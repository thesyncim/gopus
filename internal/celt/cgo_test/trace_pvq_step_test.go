// Package cgo traces PVQ encoding step by step
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTracePVQStepByStep traces the PVQ encoding step by step to find divergence.
func TestTracePVQStepByStep(t *testing.T) {
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

	// Decode libopus packet to get allocation info
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	rdLib.DecodeBit(3)  // transient
	intra := rdLib.DecodeBit(3)

	// Decode coarse energy
	goDecLib := celt.NewDecoder(1)
	coarseLib := goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, intra == 1, lm)

	// Decode TF
	tfResLib := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		tfResLib[i] = rdLib.DecodeBit(1)
	}
	rdLib.DecodeBit(1) // tf_select

	// Decode spread
	spreadICDF := []uint8{25, 23, 2, 0}
	spreadLib := rdLib.DecodeICDF(spreadICDF, 5)

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

	t.Logf("Spread from libopus: %d", spreadLib)
	t.Logf("Coarse energies (first 5): %.4f, %.4f, %.4f, %.4f, %.4f",
		coarseLib[0], coarseLib[1], coarseLib[2], coarseLib[3], coarseLib[4])

	// Now compute gopus normalized coefficients
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Apply pre-emphasis and compute MDCT
	preemph := goEnc.ApplyPreemphasisWithScaling(pcm64)
	shortBlocks := mode.ShortBlocks
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, goEnc.OverlapBuffer(), shortBlocks)

	// Compute band energies
	gopusEnergies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Get normalized coefficients
	gopusNorm := goEnc.NormalizeBandsToArray(mdctCoeffs, gopusEnergies, nbBands, frameSize)

	t.Log("")
	t.Log("=== Tracing first few bands ===")

	M := 1 << lm
	B := 1
	if shortBlocks > 1 {
		B = shortBlocks
	}

	// Decode PVQ indices from libopus
	tellBeforePVQ := rdLib.Tell()
	t.Logf("PVQ starts at bit %d", tellBeforePVQ)

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

		// Get normalized coefficients for this band
		normCoeffs := make([]float64, n)
		copy(normCoeffs, gopusNorm[bandStart:bandEnd])

		t.Logf("")
		t.Logf("=== Band %d: n=%d, k=%d, bits=%d ===", band, n, k, pulsesBits>>bitRes)
		t.Logf("First 5 normalized coefficients BEFORE exp_rotation:")
		for i := 0; i < 5 && i < n; i++ {
			t.Logf("  [%d] = %+.10f", i, normCoeffs[i])
		}

		// Apply exp_rotation (forward direction as encoder does)
		celt.ExpRotationExport(normCoeffs, n, 1, B, k, spreadLib)

		t.Logf("First 5 normalized coefficients AFTER exp_rotation:")
		for i := 0; i < 5 && i < n; i++ {
			t.Logf("  [%d] = %+.10f", i, normCoeffs[i])
		}

		// Now apply PVQ search
		pulses, yy := celt.OpPVQSearchExport(normCoeffs, k)
		t.Logf("PVQ search result (yy=%.4f):", yy)
		t.Logf("First 10 pulses:")
		for i := 0; i < 10 && i < len(pulses); i++ {
			t.Logf("  [%d] = %d", i, pulses[i])
		}

		// Encode pulses to get index
		gopusIdx := celt.EncodePulses(pulses, n, k)
		vSize := celt.PVQ_V(n, k)

		// Decode libopus index
		libIdx := rdLib.DecodeUniform(vSize)

		// Decode libopus pulses from index
		libPulses := celt.DecodePulses(libIdx, n, k)

		t.Logf("Indices: gopus=%d, libopus=%d (vSize=%d)", gopusIdx, libIdx, vSize)
		if gopusIdx != libIdx {
			t.Logf("  INDICES DIFFER!")
			t.Logf("  libopus pulses:")
			for i := 0; i < 10 && i < len(libPulses); i++ {
				marker := ""
				if i < len(pulses) && pulses[i] != libPulses[i] {
					marker = " <-- DIFF"
				}
				t.Logf("    [%d] = %d%s", i, libPulses[i], marker)
			}
		}
	}

	// Show rotation parameters
	t.Log("")
	t.Log("=== Exp Rotation Parameters ===")
	for band := 0; band < 3; band++ {
		bandStart := celt.EBands[band] * M
		bandEnd := celt.EBands[band+1] * M
		n := bandEnd - bandStart
		pulsesBits := allocResultLib.BandBits[band]
		q := celt.BitsToPulsesExport(band, lm, pulsesBits)
		k := celt.GetPulsesExport(q)

		// Check if rotation would be skipped
		if 2*k >= n || spreadLib == 0 {
			t.Logf("Band %d: rotation SKIPPED (2*k=%d >= n=%d or spread=%d)", band, 2*k, n, spreadLib)
		} else {
			spreadFactor := []int{15, 10, 5}[spreadLib-1]
			gain := float64(n) / float64(n+spreadFactor*k)
			theta := 0.5 * gain * gain
			c := math.Cos(0.5 * math.Pi * theta)
			s := math.Sin(0.5 * math.Pi * theta)
			t.Logf("Band %d: n=%d, k=%d, spread=%d, spreadFactor=%d, gain=%.6f, theta=%.6f, c=%.6f, s=%.6f",
				band, n, k, spreadLib, spreadFactor, gain, theta, c, s)
		}
	}
}

// TestCompareExpRotation compares exp_rotation between gopus and a manual implementation.
func TestCompareExpRotation(t *testing.T) {
	// Test with known values
	n := 8
	k := 3
	spread := 2 // spreadNormal
	B := 1

	// Create test vector
	x := make([]float64, n)
	for i := range x {
		x[i] = float64(i+1) * 0.1
	}

	// Copy for comparison
	xCopy := make([]float64, n)
	copy(xCopy, x)

	t.Log("Input vector:")
	for i, v := range x {
		t.Logf("  [%d] = %.10f", i, v)
	}

	// Apply gopus exp_rotation
	celt.ExpRotationExport(x, n, 1, B, k, spread)

	t.Log("")
	t.Log("After gopus exp_rotation (dir=1):")
	for i, v := range x {
		t.Logf("  [%d] = %.10f", i, v)
	}

	// Manual calculation of exp_rotation parameters
	spreadFactor := []int{15, 10, 5}[spread-1]
	gain := float64(n) / float64(n+spreadFactor*k)
	theta := 0.5 * gain * gain
	c := math.Cos(0.5 * math.Pi * theta)
	s := math.Sin(0.5 * math.Pi * theta)

	t.Log("")
	t.Logf("Parameters: spreadFactor=%d, gain=%.10f, theta=%.10f", spreadFactor, gain, theta)
	t.Logf("c=%.10f, s=%.10f", c, s)

	// Manual expRotation1 forward pass
	ms := -s
	for i := 0; i < n-1; i++ {
		x1 := xCopy[i]
		x2 := xCopy[i+1]
		xCopy[i+1] = c*x2 + s*x1
		xCopy[i] = c*x1 + ms*x2
	}
	// Backward pass
	for i := n - 3; i >= 0; i-- {
		x1 := xCopy[i]
		x2 := xCopy[i+1]
		xCopy[i+1] = c*x2 + s*x1
		xCopy[i] = c*x1 + ms*x2
	}

	t.Log("")
	t.Log("After manual exp_rotation:")
	for i, v := range xCopy {
		t.Logf("  [%d] = %.10f (diff=%.15f)", i, v, x[i]-v)
	}
}

// TestCompareNormalizedCoeffsToLibopus compares normalized coefficients from gopus encoder
// with what we'd expect based on libopus band energies.
func TestCompareNormalizedCoeffsToLibopus(t *testing.T) {
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

	// Compute gopus MDCT and normalized coefficients
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	preemph := goEnc.ApplyPreemphasisWithScaling(pcm64)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, goEnc.OverlapBuffer(), shortBlocks)
	gopusEnergies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	gopusLinearE := celt.ComputeLinearBandAmplitudes(mdctCoeffs, nbBands, frameSize)

	// Compute libopus equivalents using gopus MDCT
	mdctF32 := make([]float32, len(mdctCoeffs))
	for i, v := range mdctCoeffs {
		mdctF32[i] = float32(v)
	}

	libLinearE := ComputeLibopusBandEnergyLinear(mdctF32, nbBands, frameSize, lm)

	t.Log("=== Linear Band Amplitudes (used for normalization) ===")
	M := 1 << lm
	for band := 0; band < 5; band++ {
		bandStart := celt.EBands[band] * M
		bandEnd := celt.EBands[band+1] * M

		t.Logf("Band %d [%d-%d]: gopus=%.10f, lib=%.10f, diff=%.15f",
			band, bandStart, bandEnd, gopusLinearE[band], libLinearE[band], gopusLinearE[band]-float64(libLinearE[band]))
	}

	t.Log("")
	t.Log("=== Normalized Coefficients Comparison ===")
	// Get gopus normalized coefficients
	gopusNorm := goEnc.NormalizeBandsToArray(mdctCoeffs, gopusEnergies, nbBands, frameSize)

	// Normalize using libopus function
	libLinearEF32 := make([]float32, nbBands)
	for i, v := range libLinearE {
		libLinearEF32[i] = v
	}
	libNorm := NormaliseLibopusBands(mdctF32, libLinearEF32, frameSize, nbBands, lm)

	for band := 0; band < 3; band++ {
		bandStart := celt.EBands[band] * M
		bandEnd := celt.EBands[band+1] * M
		n := bandEnd - bandStart

		t.Logf("Band %d (first 5 of %d coefficients):", band, n)
		for i := 0; i < 5 && i < n; i++ {
			idx := bandStart + i
			diff := gopusNorm[idx] - float64(libNorm[idx])
			marker := ""
			if math.Abs(diff) > 1e-6 {
				marker = " <-- DIFF"
			}
			t.Logf("  [%d]: gopus=%+.10f, lib=%+.10f, diff=%+.15f%s",
				idx, gopusNorm[idx], libNorm[idx], diff, marker)
		}

		// Compute L2 norm of both
		gopusNormSq := 0.0
		libNormSq := 0.0
		for i := bandStart; i < bandEnd; i++ {
			gopusNormSq += gopusNorm[i] * gopusNorm[i]
			libNormSq += float64(libNorm[i]) * float64(libNorm[i])
		}
		t.Logf("  L2 norm: gopus=%.10f, lib=%.10f", math.Sqrt(gopusNormSq), math.Sqrt(libNormSq))
	}
}
