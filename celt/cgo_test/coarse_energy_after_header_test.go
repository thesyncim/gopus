//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests coarse energy encoding after header.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestCoarseEnergyAfterHeader traces coarse energy encoding starting from header state.
func TestCoarseEnergyAfterHeader(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	t.Log("=== Coarse Energy After Header Test ===")
	t.Log("")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Create encoder
	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(bitrate)

	// Apply pre-emphasis
	preemph := enc.ApplyPreemphasisWithScaling(samples)

	// Transient detection
	overlap := celt.Overlap
	transientInput := make([]float64, overlap+frameSize)
	copy(transientInput[overlap:], preemph)
	result := enc.TransientAnalysis(transientInput, frameSize+overlap, false)
	transient := result.IsTransient

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	t.Logf("Transient: %v, shortBlocks: %d", transient, shortBlocks)

	// MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)
	t.Logf("MDCT: %d coefficients", len(mdctCoeffs))

	// Band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Initialize range encoder
	targetBits := bitrate * frameSize / sampleRate
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Encode header flags
	re.EncodeBit(0, 15) // silence=0
	re.EncodeBit(0, 1)  // postfilter=0
	transientBit := 0
	if transient {
		transientBit = 1
	}
	re.EncodeBit(transientBit, 3) // transient
	re.EncodeBit(0, 3)            // intra=0

	t.Logf("After header: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode coarse energy
	enc.SetRangeEncoder(re)
	enc.SetFrameBitsForTest(targetBits)
	intra := false // Match libopus
	quantizedEnergies := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	t.Logf("After coarse energy: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Finalize
	gopusBytes := re.Done()

	// Show first 10 bytes
	t.Log("")
	t.Log("=== Gopus Output ===")
	showLen := 10
	if showLen > len(gopusBytes) {
		showLen = len(gopusBytes)
	}
	t.Logf("First %d bytes: %02X", showLen, gopusBytes[:showLen])

	// Show energies and quantized values
	t.Log("")
	t.Log("=== Energy Analysis ===")
	t.Log("Band | Energy   | Quantized | Diff")
	t.Log("-----+----------+-----------+------")
	for i := 0; i < 10 && i < len(energies); i++ {
		q := 0.0
		if i < len(quantizedEnergies) {
			q = quantizedEnergies[i]
		}
		t.Logf("%4d | %8.4f | %9.4f | %5.2f", i, energies[i], q, energies[i]-q)
	}

	// Now encode with libopus for header + Laplace sequence
	// First compute the qi values gopus would encode
	t.Log("")
	t.Log("=== QI Values ===")

	// The qi values come from EncodeCoarseEnergy - let me trace them
	// For now, just show the energy values that go into qi computation
	t.Log("Input energies for QI computation:")
	for i := 0; i < 10 && i < len(energies); i++ {
		t.Logf("  Band %d: energy=%.4f", i, energies[i])
	}
}
