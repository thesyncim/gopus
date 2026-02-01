// Package cgo traces what normalization factor gopus vs libopus uses
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceNormalizationSource identifies whether gopus uses actual or quantized energy for normalization.
func TestTraceNormalizationSource(t *testing.T) {
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
	lm := mode.LM
	shortBlocks := mode.ShortBlocks
	M := 1 << lm

	// Gopus pipeline
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	gopusPreemph := goEnc.ApplyPreemphasisWithScaling(pcm64)
	gopusMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, goEnc.OverlapBuffer(), shortBlocks)
	gopusEnergies := goEnc.ComputeBandEnergies(gopusMDCT, nbBands, frameSize)
	gopusLinearE := celt.ComputeLinearBandAmplitudes(gopusMDCT, nbBands, frameSize)

	// What gopus uses for normalization (this is the key)
	gopusNorm := goEnc.NormalizeBandsToArray(gopusMDCT, gopusEnergies, nbBands, frameSize)

	// Get libopus encoded coarse energy
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

	// Decode libopus packet to get coarse energy
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)
	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	rdLib.DecodeBit(3)  // transient
	intra := rdLib.DecodeBit(3)

	goDecLib := celt.NewDecoder(1)
	coarseLib := goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, intra == 1, lm)

	// Decode rest to get to fine energy
	for i := 0; i < nbBands; i++ {
		rdLib.DecodeBit(1) // TF
	}
	rdLib.DecodeBit(1) // tf_select
	spreadICDF := []uint8{25, 23, 2, 0}
	rdLib.DecodeICDF(spreadICDF, 5) // spread

	// Dynalloc
	bitRes := 3
	capsLib := celt.InitCaps(nbBands, lm, 1)
	offsetsLib := make([]int, nbBands)
	targetBytes := 159
	targetBits := targetBytes * 8
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

	// Trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	trimLib := rdLib.DecodeICDF(trimICDF, 7)

	// Allocation
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
	fineQLib := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		fineBits := allocResultLib.FineBits[i]
		if fineBits == 0 {
			continue
		}
		q := rdLib.DecodeRawBits(uint(fineBits))
		fineQLib[i] = int(q)
	}

	// Compute libopus quantized energies (coarse + fine)
	eMeans := celt.GetEMeans()
	libQuantizedEnergies := make([]float64, nbBands)
	for band := 0; band < nbBands; band++ {
		// Coarse energy
		quantizedE := coarseLib[band]

		// Add fine energy refinement
		fineBits := allocResultLib.FineBits[band]
		if fineBits > 0 {
			ft := 1 << fineBits
			q := fineQLib[band]
			offset := (float64(q)+0.5)/float64(ft) - 0.5
			quantizedE += offset * 6.0 // DB6 = 6.0 in log2 units (1 = 6dB)
		}

		libQuantizedEnergies[band] = quantizedE
	}

	t.Log("=== Band 0 Normalization Analysis ===")
	band := 0
	bandStart := celt.EBands[band] * M
	bandEnd := celt.EBands[band+1] * M
	n := bandEnd - bandStart

	// Compute what normalization factor gopus uses
	gopusNormFactor := 1.0
	if gopusLinearE[band] > 0 {
		gopusNormFactor = 1.0 / gopusLinearE[band]
	}

	// Compute what libopus would use for normalization (from quantized energy)
	// libopus gain = 2^(quantized_energy + eMeans)
	libQuantizedWithMean := libQuantizedEnergies[band] + eMeans[band]
	libGain := math.Exp2(libQuantizedWithMean / 6.0) // Convert from log2*6 to linear (divide by DB6=6)
	libNormFactor := 1.0
	if libGain > 0 {
		libNormFactor = 1.0 / libGain
	}

	t.Logf("Band %d width: %d", band, n)
	t.Logf("")
	t.Logf("Gopus computed energy (log2, mean-relative): %.6f", gopusEnergies[band])
	t.Logf("Libopus quantized energy (log2, mean-relative): %.6f", libQuantizedEnergies[band])
	t.Logf("Energy difference: %.6f", gopusEnergies[band]-libQuantizedEnergies[band])
	t.Logf("")
	t.Logf("Gopus linear amplitude (from MDCT): %.6f", gopusLinearE[band])
	t.Logf("Gopus normalization factor: %.10f", gopusNormFactor)
	t.Logf("")
	t.Logf("Libopus quantized energy + eMeans: %.6f", libQuantizedWithMean)
	t.Logf("Libopus gain from quantized: %.6f", libGain)
	t.Logf("Libopus normalization factor: %.10f", libNormFactor)
	t.Logf("")
	t.Logf("Ratio of normalization factors (gopus/lib): %.6f", gopusNormFactor/libNormFactor)

	// Check actual normalized coefficients
	t.Log("")
	t.Log("=== Normalized Coefficients (first 5) ===")
	t.Log("Index | MDCT coeff | Gopus norm | Should be (using lib factor)")
	for i := 0; i < 5 && i < n; i++ {
		idx := bandStart + i
		mdct := gopusMDCT[idx]
		gopusN := gopusNorm[idx]
		shouldBe := mdct * libNormFactor
		t.Logf("%5d | %+.6f | %+.10f | %+.10f", idx, mdct, gopusN, shouldBe)
	}

	// Now compute L2 norm of gopus normalized band
	gopusL2 := 0.0
	for i := bandStart; i < bandEnd; i++ {
		gopusL2 += gopusNorm[i] * gopusNorm[i]
	}
	gopusL2 = math.Sqrt(gopusL2)
	t.Logf("")
	t.Logf("Gopus normalized band L2 norm: %.10f (should be 1.0)", gopusL2)

	// KEY INSIGHT: What should libopus use?
	// libopus normalise_bands() uses compute_band_energies() output directly.
	// compute_band_energies() returns sqrt(sum of squares) = ACTUAL linear amplitude.
	// So libopus ALSO normalizes using actual linear amplitude, NOT quantized energy!
	t.Log("")
	t.Log("=== KEY INSIGHT ===")
	t.Log("libopus normalise_bands() uses compute_band_energies() output directly.")
	t.Log("This is the ACTUAL linear amplitude, not the quantized energy.")
	t.Log("So both encoders should normalize the same way!")
	t.Log("")
	t.Log("The issue must be elsewhere - let's check what libopus actually encodes...")

	// Let's verify by checking what direction libopus encoded
	// Decode PVQ indices
	for i := 0; i < nbBands; i++ {
		fineBits := allocResultLib.FineBits[i]
		if fineBits > 0 {
			// Already decoded above
		}
	}

	// Decode PVQ index for band 0
	pulsesBits := allocResultLib.BandBits[band]
	q := celt.BitsToPulsesExport(band, lm, pulsesBits)
	k := celt.GetPulsesExport(q)

	if k > 0 {
		vSize := celt.PVQ_V(n, k)
		libIdx := rdLib.DecodeUniform(vSize)
		libPulses := celt.DecodePulses(libIdx, n, k)

		// Convert libopus pulses to direction vector
		libPulseNorm := 0.0
		for _, p := range libPulses {
			libPulseNorm += float64(p * p)
		}
		libPulseNorm = math.Sqrt(libPulseNorm)

		t.Log("")
		t.Logf("Band 0 PVQ: k=%d, vSize=%d, libIdx=%d", k, vSize, libIdx)
		t.Log("Libopus encoded direction (first 5):")
		for i := 0; i < 5 && i < len(libPulses); i++ {
			dir := float64(libPulses[i]) / libPulseNorm
			t.Logf("  [%d] = %+.10f (pulse=%d)", i, dir, libPulses[i])
		}

		// Compare with gopus normalized direction
		t.Log("Gopus normalized direction (first 5):")
		for i := 0; i < 5 && i < n; i++ {
			t.Logf("  [%d] = %+.10f", bandStart+i, gopusNorm[bandStart+i])
		}

		// Compute dot product
		dotProduct := 0.0
		for i := 0; i < n; i++ {
			libDir := float64(libPulses[i]) / libPulseNorm
			dotProduct += libDir * gopusNorm[bandStart+i]
		}
		t.Logf("")
		t.Logf("Dot product between gopus and libopus directions: %.10f", dotProduct)
		t.Logf("(Should be close to 1.0 if same direction)")
	}
}
