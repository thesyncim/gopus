// Package cgo tests QI values with transient mode (shortBlocks=8).
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTransientModeQITrace tests QI values with transient mode (shortBlocks=8).
func TestTransientModeQITrace(t *testing.T) {
	frameSize := 960
	sampleRate := 48000

	// Generate 440Hz sine wave
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	t.Log("=== Transient Mode QI Trace ===")
	t.Log("Using shortBlocks=8 (transient) and intra=false (inter mode)")
	t.Log("")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	shortBlocks := mode.ShortBlocks // = 8

	t.Logf("Frame size: %d, LM: %d, shortBlocks: %d", frameSize, lm, shortBlocks)

	// Create encoder
	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(64000)

	// Apply pre-emphasis
	preemph := enc.ApplyPreemphasisWithScaling(samples)

	// MDCT with SHORT BLOCKS (transient mode)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)
	t.Logf("MDCT: %d coefficients (shortBlocks=%d)", len(mdctCoeffs), shortBlocks)

	// Band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Show energies
	t.Log("")
	t.Log("=== Energies (transient mode, shortBlocks=8) ===")
	for i := 0; i < 10 && i < len(energies); i++ {
		t.Logf("  Band %d: energy=%.4f", i, energies[i])
	}

	// Compare with non-transient mode
	t.Log("")
	t.Log("=== Energies (non-transient mode, shortBlocks=1) ===")
	mdctLong := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), 1)
	energiesLong := enc.ComputeBandEnergies(mdctLong, nbBands, frameSize)
	for i := 0; i < 10 && i < len(energiesLong); i++ {
		diff := energies[i] - energiesLong[i]
		t.Logf("  Band %d: energy=%.4f (diff from transient: %.4f)", i, energiesLong[i], diff)
	}

	// Trace QI values with intra=false (inter mode)
	t.Log("")
	t.Log("=== QI Trace (intra=false, inter mode) ===")
	targetBits := 64000 * frameSize / 48000
	intra := false

	gopusTraces, goBytes := traceCoarseEnergyQI(energies, nbBands, intra, lm, targetBits)
	libTraces, libBytes, err := traceLibopusCoarseEnergyQI(energies, nbBands, intra, lm, targetBits)
	if err != nil {
		t.Fatalf("libopus trace failed: %v", err)
	}

	// Compare
	t.Log("")
	t.Log("Band | x(energy) | qi(gopus) | qi(libopus) | Match")
	t.Log("-----+-----------+-----------+-------------+------")

	mismatchCount := 0
	for i := 0; i < 10 && i < len(gopusTraces); i++ {
		goT := gopusTraces[i]
		libT := libTraces[i]

		match := "YES"
		if goT.QI != libT.QI {
			match = "NO"
			mismatchCount++
		}

		t.Logf("%4d | %9.4f | %9d | %11d | %s",
			goT.Band, goT.X, goT.QI, libT.QI, match)
	}

	t.Log("")
	if mismatchCount > 0 {
		t.Logf("MISMATCH: %d qi values differ", mismatchCount)
	} else {
		t.Log("All qi values match")
	}

	// Compare bytes
	t.Log("")
	t.Logf("Gopus bytes: %02X", goBytes[:minIntTransient(10, len(goBytes))])
	t.Logf("Libopus bytes: %02X", libBytes[:minIntTransient(10, len(libBytes))])
}

func minIntTransient(a, b int) int {
	if a < b {
		return a
	}
	return b
}
