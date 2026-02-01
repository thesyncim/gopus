// Package cgo traces the spread decision algorithm.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTraceSpreadDecision traces spread decision computation step by step.
func TestTraceSpreadDecision(t *testing.T) {
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

	// Now trace gopus spread decision
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	shortBlocks := mode.ShortBlocks // transient

	// Step 1: Get normalized MDCT from gopus (matching libopus pre-emphasis)
	libPreemph := ApplyLibopusPreemphasis(pcm32, 0.85)
	libPreemphF64 := make([]float64, len(libPreemph))
	for i, v := range libPreemph {
		libPreemphF64[i] = float64(v)
	}

	// Compute MDCT with history (zeros for first frame)
	history := make([]float64, celt.Overlap)
	mdctCoeffs := celt.ComputeMDCTWithHistory(libPreemphF64, history, shortBlocks)
	t.Logf("MDCT coefficients count: %d", len(mdctCoeffs))

	// Step 2: Compute band energies
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)

	energies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	t.Logf("Band energies (first 5):")
	for i := 0; i < 5 && i < len(energies); i++ {
		t.Logf("  Band %d: %.4f", i, energies[i])
	}

	// Step 3: Normalize bands
	normCoeffs := goEnc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)
	t.Logf("Normalized coefficients count: %d", len(normCoeffs))

	// Convert to float32 for C function
	normCoeffsF32 := make([]float32, len(normCoeffs))
	for i, v := range normCoeffs {
		normCoeffsF32[i] = float32(v)
	}

	// Show some normalized coeffs
	t.Logf("Normalized coeffs (first 16):")
	for i := 0; i < 16 && i < len(normCoeffs); i++ {
		t.Logf("  [%d]: %.6f", i, normCoeffs[i])
	}

	// Step 4: Compute spread weights
	spreadWeights := celt.ComputeSpreadWeights(energies, nbBands, 1, 16)
	t.Logf("Spread weights (first 10):")
	for i := 0; i < 10 && i < len(spreadWeights); i++ {
		t.Logf("  Band %d: %d", i, spreadWeights[i])
	}

	// Step 5: Compute using C version of spreading_decision
	// Initial state: libopus uses tonal_average=256, spread_decision=SPREAD_NORMAL (2)
	// Reference: celt_encoder.c line 3088-3089
	libResult := ComputeLibopusSpreadDecision(
		normCoeffsF32,
		len(normCoeffsF32),
		nbBands,
		lm,
		spreadWeights,
		256,   // tonalAverage - libopus initializes to 256
		2,     // spreadDecision - libopus initializes to SPREAD_NORMAL
		0,     // hfAverage
		0,     // tapsetDecision
		false, // updateHF
	)

	t.Logf("")
	t.Logf("=== C spreading_decision result ===")
	t.Logf("Decision: %d", libResult.Decision)
	t.Logf("Sum (after hysteresis): %d", libResult.Sum)
	t.Logf("Sum (before averaging): %d", libResult.SumBefore)
	t.Logf("Updated TonalAverage: %d", libResult.TonalAverage)
	t.Logf("Updated SpreadDecision: %d", libResult.SpreadDecision)

	// Also compute what gopus would do with correct initial state
	// tonalAverage=256, spreadDecision=2
	sumNorm := 745
	tonalAvg := 256
	sumAvg := (sumNorm + tonalAvg) >> 1
	lastDec := 2
	sumHyst := (3*sumAvg + ((3 - lastDec) << 7) + 64 + 2) >> 2
	t.Logf("")
	t.Logf("=== Manual calc with libopus init state (tonalAvg=256, lastDec=2) ===")
	t.Logf("sumNorm=%d, sumAvg=(sumNorm+256)>>1=%d", sumNorm, sumAvg)
	t.Logf("sumHyst=(3*%d + ((3-2)<<7) + 64 + 2)>>2 = (%d + 128 + 66)>>2 = %d", sumAvg, 3*sumAvg, sumHyst)

	// Step 6: Manual spread decision computation
	M := frameSize / 120
	N0 := M * 120
	_ = N0

	// Check last band width
	lastBandWidth := celt.ScaledBandWidth(nbBands-1, frameSize)
	t.Logf("Last band width: %d (if <= 8, returns SPREAD_NONE)", lastBandWidth)

	// Compute tcount for each band
	t.Log("")
	t.Log("=== Per-band tcount analysis ===")
	sum := 0
	nbBandsTotal := 0
	for band := 0; band < nbBands; band++ {
		bandStart := celt.ScaledBandStart(band, frameSize)
		bandEnd := celt.ScaledBandEnd(band, frameSize)
		N := bandEnd - bandStart

		if N <= 8 {
			continue
		}

		xOffset := bandStart
		if xOffset+N > len(normCoeffs) {
			continue
		}

		tcount := [3]int{0, 0, 0}
		Nf := float64(N)

		for j := 0; j < N; j++ {
			x := normCoeffs[xOffset+j]
			x2N := x * x * Nf

			if x2N < 0.25 {
				tcount[0]++
			}
			if x2N < 0.0625 {
				tcount[1]++
			}
			if x2N < 0.015625 {
				tcount[2]++
			}
		}

		tmp := 0
		if 2*tcount[2] >= N {
			tmp++
		}
		if 2*tcount[1] >= N {
			tmp++
		}
		if 2*tcount[0] >= N {
			tmp++
		}

		sum += tmp * spreadWeights[band]
		nbBandsTotal += spreadWeights[band]

		t.Logf("Band %d (N=%d): tcount=[%d,%d,%d] tmp=%d weight=%d sum_contrib=%d",
			band, N, tcount[0], tcount[1], tcount[2], tmp, spreadWeights[band], tmp*spreadWeights[band])
	}

	t.Logf("")
	t.Logf("sum before normalization: %d", sum)
	t.Logf("nbBandsTotal: %d", nbBandsTotal)

	// Normalize sum to Q8
	if nbBandsTotal > 0 {
		sum = (sum << 8) / nbBandsTotal
	}
	t.Logf("sum after normalization (Q8): %d", sum)

	// For first frame, tonalAverage = 0
	tonalAverage := 0
	sum = (sum + tonalAverage) >> 1
	t.Logf("sum after averaging with tonalAverage=0: %d", sum)

	// Apply hysteresis (lastDecision = 0 for first frame)
	lastDecision := 0
	sum = (3*sum + ((3 - lastDecision) << 7) + 64 + 2) >> 2
	t.Logf("sum after hysteresis (lastDecision=0): %d", sum)

	// Make decision
	var decision int
	if sum < 80 {
		decision = 3 // SPREAD_AGGRESSIVE
	} else if sum < 256 {
		decision = 2 // SPREAD_NORMAL
	} else if sum < 384 {
		decision = 1 // SPREAD_LIGHT
	} else {
		decision = 0 // SPREAD_NONE
	}
	t.Logf("Thresholds: <80=AGGRESSIVE, <256=NORMAL, <384=LIGHT, else=NONE")
	t.Logf("Final decision: %d (sum=%d)", decision, sum)

	// Now call the actual gopus spread decision
	goSpread := goEnc.SpreadingDecisionWithWeights(normCoeffs, nbBands, 1, frameSize, false, spreadWeights)
	t.Logf("")
	t.Logf("=== Summary ===")
	t.Logf("Manual calculation: %d", decision)
	t.Logf("Gopus SpreadingDecisionWithWeights: %d", goSpread)
	t.Logf("C spreading_decision: %d", libResult.Decision)

	if goSpread != libResult.Decision {
		t.Errorf("SPREAD MISMATCH: gopus=%d C=%d", goSpread, libResult.Decision)
	}
}
