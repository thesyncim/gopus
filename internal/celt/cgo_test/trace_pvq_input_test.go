//go:build trace
// +build trace

// Package cgo traces PVQ input differences between gopus and libopus
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTracePVQInputDifference traces what input libopus PVQ sees vs gopus.
func TestTracePVQInputDifference(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]

	// Get frame parameters
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	targetBytes := 159
	targetBits := targetBytes * 8

	// Decode libopus packet to get allocation and PVQ indices
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	rdLib.DecodeBit(3)  // transient
	intra := rdLib.DecodeBit(3)

	goDecLib := celt.NewDecoder(1)
	coarseLib := goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, intra == 1, lm)

	// Decode TF
	for i := 0; i < nbBands; i++ {
		rdLib.DecodeBit(1)
	}
	rdLib.DecodeBit(1) // tf_select

	// Decode spread
	spreadICDF := []uint8{25, 23, 2, 0}
	spreadLib := rdLib.DecodeICDF(spreadICDF, 5)
	_ = spreadLib

	// Decode dynalloc
	bitRes := 3
	capsLib := celt.InitCaps(nbBands, lm, 1)
	offsetsLib := make([]int, nbBands)
	totalBitsQ3ForDynalloc := targetBits << bitRes
	dynallocLogp := 6
	totalBoost := 0
	tellFracDynalloc := rdLib.TellFrac()

	for i := 0; i < nbBands; i++ {
		width := celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := 6 << bitRes
		if width > innerMax {
			innerMax = width
		}
		quanta := width << bitRes
		if quanta > innerMax {
			quanta = innerMax
		}

		dynallocLoopLogp := dynallocLogp
		boost := 0

		for j := 0; tellFracDynalloc+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoost && boost < capsLib[i]; j++ {
			flag := rdLib.DecodeBit(uint(dynallocLoopLogp))
			tellFracDynalloc = rdLib.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoost += quanta
			dynallocLoopLogp = 1
		}

		if boost > 0 && dynallocLogp > 2 {
			dynallocLogp--
		}
		offsetsLib[i] = boost
	}

	// Decode trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	trimLib := rdLib.DecodeICDF(trimICDF, 7)

	// Compute allocation
	bitsUsedLib := rdLib.TellFrac()
	totalBitsQ3Lib := (targetBits << bitRes) - bitsUsedLib - 1
	antiCollapseRsv := 1 << bitRes
	totalBitsQ3Lib -= antiCollapseRsv

	allocResultLib := celt.ComputeAllocationWithDecoder(
		rdLib, totalBitsQ3Lib>>bitRes,
		nbBands, 1, capsLib, offsetsLib, trimLib,
		nbBands, false, lm,
	)

	// Decode fine energy
	for i := 0; i < nbBands; i++ {
		fineBits := allocResultLib.FineBits[i]
		if fineBits == 0 {
			continue
		}
		rdLib.DecodeRawBits(uint(fineBits))
	}

	M := 1 << lm

	// Now compare the key insight: libopus's quantized band energies (coarse energy)
	// determine normalization. Let's see what normalization factor libopus uses.
	t.Log("=== Understanding the divergence ===")
	t.Log("")
	t.Log("libopus coarse energies (log2 scale, mean-relative):")
	for band := 0; band < 5; band++ {
		t.Logf("  Band %d: %.6f", band, coarseLib[band])
	}

	// Convert coarse energies to linear amplitudes (what libopus uses for normalization)
	// The formula is: linear_amp = 2^(coarse_energy)
	// But wait - libopus also adds back the mean (eMeans)
	eMeans := celt.GetEMeans()
	t.Log("")
	t.Log("libopus band amplitudes (linear):")
	libLinearAmps := make([]float64, nbBands)
	for band := 0; band < 5; band++ {
		// Add back mean and convert from log2 to linear
		rawEnergy := coarseLib[band] + eMeans[band]
		linearAmp := math.Exp2(rawEnergy / 2.0) // divide by 2 because it's log2 of energy (squared)
		libLinearAmps[band] = linearAmp
		t.Logf("  Band %d: raw_energy=%.6f, linear_amp=%.6f", band, rawEnergy, linearAmp)
	}

	// Now compute gopus values
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	preemph := goEnc.ApplyPreemphasisWithScaling(pcm64)
	shortBlocks := mode.ShortBlocks
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, goEnc.OverlapBuffer(), shortBlocks)
	gopusEnergies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	gopusLinearE := celt.ComputeLinearBandAmplitudes(mdctCoeffs, nbBands, frameSize)

	t.Log("")
	t.Log("gopus band energies (log2, mean-relative) and linear amplitudes:")
	for band := 0; band < 5; band++ {
		t.Logf("  Band %d: log2_energy=%.6f, linear_amp=%.6f", band, gopusEnergies[band], gopusLinearE[band])
	}

	// KEY INSIGHT: The difference in normalization leads to different PVQ vectors!
	// If libopus quantizes energy differently, the normalized coefficients will differ.
	t.Log("")
	t.Log("=== Comparing MDCT coefficients BEFORE normalization ===")
	for band := 0; band < 3; band++ {
		bandStart := celt.EBands[band] * M
		bandEnd := celt.EBands[band+1] * M
		n := bandEnd - bandStart

		t.Logf("Band %d (first 5 of %d coefficients):", band, n)
		for i := 0; i < 5 && i < n; i++ {
			idx := bandStart + i
			t.Logf("  [%d] = %.10f", idx, mdctCoeffs[idx])
		}
	}

	// Show what normalization factor is used
	t.Log("")
	t.Log("=== Normalization factors ===")
	for band := 0; band < 3; band++ {
		bandStart := celt.EBands[band] * M
		bandEnd := celt.EBands[band+1] * M

		// Compute sum of squares of MDCT coefficients
		sumSq := 0.0
		for i := bandStart; i < bandEnd; i++ {
			sumSq += mdctCoeffs[i] * mdctCoeffs[i]
		}
		actualLinear := math.Sqrt(sumSq)

		// What gopus uses
		gopusNormFactor := 1.0
		if gopusLinearE[band] > 0 {
			gopusNormFactor = 1.0 / gopusLinearE[band]
		}

		// What would libopus use based on decoded coarse energy
		libNormFactor := 1.0
		if libLinearAmps[band] > 0 {
			libNormFactor = 1.0 / libLinearAmps[band]
		}

		t.Logf("Band %d: actual_L2=%.6f, gopus_linear=%.6f (norm=%.6f), lib_decoded_linear=%.6f (norm=%.6f)",
			band, actualLinear, gopusLinearE[band], gopusNormFactor, libLinearAmps[band], libNormFactor)
	}

	// Show PVQ pulses decoded from libopus packet
	t.Log("")
	t.Log("=== PVQ pulses from libopus packet ===")
	for band := 0; band < 3; band++ {
		bandStart := celt.EBands[band] * M
		bandEnd := celt.EBands[band+1] * M
		n := bandEnd - bandStart

		pulsesBits := allocResultLib.BandBits[band]
		q := celt.BitsToPulsesExport(band, lm, pulsesBits)
		k := celt.GetPulsesExport(q)

		if k <= 0 {
			t.Logf("Band %d: k=0, skipping", band)
			continue
		}

		vSize := celt.PVQ_V(n, k)
		libIdx := rdLib.DecodeUniform(vSize)
		libPulses := celt.DecodePulses(libIdx, n, k)

		// Compute the "direction" of the libopus pulses
		// After normalization to unit vector, this tells us what direction libopus encoded
		sumSqPulses := 0.0
		for _, p := range libPulses {
			sumSqPulses += float64(p * p)
		}
		normPulses := math.Sqrt(sumSqPulses)

		t.Logf("Band %d: n=%d, k=%d, idx=%d, ||pulses||=%.4f", band, n, k, libIdx, normPulses)
		t.Logf("  libopus pulses: %v", libPulses)

		// Convert pulses to normalized direction vector
		libDirection := make([]float64, n)
		for i, p := range libPulses {
			libDirection[i] = float64(p) / normPulses
		}
		t.Logf("  libopus direction: [%.4f, %.4f, %.4f, %.4f, %.4f, ...]",
			libDirection[0], libDirection[1], libDirection[2], libDirection[3], libDirection[4])

		// Compare with gopus normalized coefficients
		gopusNorm := goEnc.NormalizeBandsToArray(mdctCoeffs, gopusEnergies, nbBands, frameSize)
		gopusDirection := gopusNorm[bandStart:bandEnd]
		t.Logf("  gopus direction:  [%.4f, %.4f, %.4f, %.4f, %.4f, ...]",
			gopusDirection[0], gopusDirection[1], gopusDirection[2], gopusDirection[3], gopusDirection[4])

		// Compute dot product to see how similar they are
		dotProduct := 0.0
		for i := 0; i < n; i++ {
			dotProduct += libDirection[i] * gopusDirection[i]
		}
		t.Logf("  Dot product (should be ~1.0 if same direction): %.6f", dotProduct)
		t.Logf("")
	}

	// KEY REALIZATION: The issue might be that libopus uses DIFFERENT MDCT coefficients!
	// Let's check if the MDCT itself differs.
	t.Log("=== Hypothesis: MDCT coefficients differ ===")
	t.Log("Possible causes:")
	t.Log("1. Pre-emphasis filter state differs")
	t.Log("2. MDCT window differs")
	t.Log("3. Short blocks interleaving differs")
	t.Log("4. Overlap buffer state differs")
}

// TestCompareFirstBandNormalization zooms into band 0 normalization.
func TestCompareFirstBandNormalization(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	shortBlocks := mode.ShortBlocks
	lm := mode.LM
	M := 1 << lm

	// Gopus MDCT computation
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)

	preemph := goEnc.ApplyPreemphasisWithScaling(pcm64)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, goEnc.OverlapBuffer(), shortBlocks)
	gopusLinearE := celt.ComputeLinearBandAmplitudes(mdctCoeffs, nbBands, frameSize)

	// Convert to float32 for libopus comparison
	mdctF32 := make([]float32, len(mdctCoeffs))
	for i, v := range mdctCoeffs {
		mdctF32[i] = float32(v)
	}

	// Compute libopus linear energies from same MDCT
	libLinearE := ComputeLibopusBandEnergyLinear(mdctF32, nbBands, frameSize, lm)

	band := 0
	bandStart := celt.EBands[band] * M
	bandEnd := celt.EBands[band+1] * M
	n := bandEnd - bandStart

	t.Logf("Band 0: [%d, %d), n=%d", bandStart, bandEnd, n)
	t.Logf("")

	// Show MDCT coefficients for band 0
	t.Log("MDCT coefficients for band 0:")
	for i := 0; i < n; i++ {
		idx := bandStart + i
		t.Logf("  [%d] = %+.10f", i, mdctCoeffs[idx])
	}

	// Compute sum of squares
	sumSq := 0.0
	for i := bandStart; i < bandEnd; i++ {
		sumSq += mdctCoeffs[i] * mdctCoeffs[i]
	}
	actualL2 := math.Sqrt(sumSq)

	t.Logf("")
	t.Logf("Sum of squares: %.10f", sumSq)
	t.Logf("Actual L2 norm: %.10f", actualL2)
	t.Logf("Gopus linear amp: %.10f", gopusLinearE[band])
	t.Logf("Libopus linear amp: %.10f", libLinearE[band])

	// Compute normalized coefficients using ACTUAL L2 norm (what libopus does)
	t.Logf("")
	t.Log("Normalized using actual L2 norm:")
	for i := 0; i < n; i++ {
		idx := bandStart + i
		normalized := mdctCoeffs[idx] / actualL2
		t.Logf("  [%d] = %+.10f", i, normalized)
	}

	// Compute normalized coefficients using gopus linear amp
	t.Logf("")
	t.Log("Normalized using gopus linear amp:")
	for i := 0; i < n; i++ {
		idx := bandStart + i
		normalized := mdctCoeffs[idx] / gopusLinearE[band]
		t.Logf("  [%d] = %+.10f", i, normalized)
	}

	// Verify L2 norm of normalized vector
	t.Logf("")
	actualNormSumSq := 0.0
	gopusNormSumSq := 0.0
	for i := bandStart; i < bandEnd; i++ {
		actualNormSumSq += (mdctCoeffs[i] / actualL2) * (mdctCoeffs[i] / actualL2)
		gopusNormSumSq += (mdctCoeffs[i] / gopusLinearE[band]) * (mdctCoeffs[i] / gopusLinearE[band])
	}
	t.Logf("L2 norm of actual-normalized: %.10f (should be 1.0)", math.Sqrt(actualNormSumSq))
	t.Logf("L2 norm of gopus-normalized: %.10f", math.Sqrt(gopusNormSumSq))
}
