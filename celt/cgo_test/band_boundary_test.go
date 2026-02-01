//go:build cgo_libopus
// +build cgo_libopus

// Package cgo verifies band boundary calculations.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestBandBoundaries verifies band start/end indices are correct.
func TestBandBoundaries(t *testing.T) {
	// For 960-sample frame at 48kHz (20ms), scale = 960/120 = 8
	frameSize := 960

	t.Log("=== Band Boundaries for 960-sample frame ===")
	t.Log("Scale factor: frameSize/Overlap = 960/120 = 8")
	t.Log("")
	t.Log("Band | Start | End | Width | EBands[band] | EBands[band+1]")
	t.Log("-----+-------+-----+-------+--------------+---------------")

	for band := 0; band <= celt.MaxBands; band++ {
		start := celt.ScaledBandStart(band, frameSize)
		end := celt.ScaledBandEnd(band, frameSize)
		width := celt.ScaledBandWidth(band, frameSize)

		// Get raw EBands values
		ebands := celt.GetEBands(3) // LM=3 for 20ms
		ebandStart := -1
		ebandEnd := -1
		if band < len(ebands) {
			ebandStart = ebands[band] / 8 // Unscale to get original
		}
		if band+1 < len(ebands) {
			ebandEnd = ebands[band+1] / 8
		}

		t.Logf("  %2d |  %3d  | %3d |  %3d  |      %2d      |      %2d",
			band, start, end, width, ebandStart, ebandEnd)
	}

	// Verify specific bands
	t.Log("")
	t.Log("=== Verification of problematic bands ===")

	// Check that band 10 ends at band 11 start
	for band := 10; band <= 16; band++ {
		end := celt.ScaledBandEnd(band, frameSize)
		nextStart := celt.ScaledBandStart(band+1, frameSize)
		if end != nextStart {
			t.Errorf("Band %d end (%d) != band %d start (%d)", band, end, band+1, nextStart)
		}
	}
	t.Log("Band boundaries are contiguous: OK")

	// Show where higher bands are in the frequency spectrum
	t.Log("")
	t.Log("=== Frequency correspondence (48kHz) ===")
	t.Log("Band | Bin Range | Freq Range (Hz)")
	for band := 10; band <= 20; band++ {
		start := celt.ScaledBandStart(band, frameSize)
		end := celt.ScaledBandEnd(band, frameSize)
		// Each bin represents 48000/960 = 50 Hz
		freqStart := start * 50
		freqEnd := end * 50
		t.Logf("  %2d | %3d - %3d | %5d - %5d Hz", band, start, end, freqStart, freqEnd)
	}
}

// TestMDCTCoefficientCount verifies MDCT produces correct number of coefficients.
func TestMDCTCoefficientCount(t *testing.T) {
	// For 960-sample frame with 120 overlap, MDCT should produce 960 coefficients
	frameSize := 960
	overlap := celt.Overlap

	t.Logf("Frame size: %d samples", frameSize)
	t.Logf("Overlap: %d samples", overlap)
	t.Logf("Expected MDCT coefficients: %d", frameSize)

	// Test long block MDCT
	input := make([]float64, frameSize+overlap)
	for i := range input {
		input[i] = float64(i) * 0.001
	}

	longOutput := celt.MDCT(input)
	t.Logf("Long block MDCT output: %d coefficients", len(longOutput))

	if len(longOutput) != frameSize {
		t.Errorf("Long block MDCT: expected %d coefficients, got %d", frameSize, len(longOutput))
	}

	// Test short block MDCT (8 blocks)
	shortOutput := celt.MDCTShort(input, 8)
	t.Logf("Short block MDCT output: %d coefficients", len(shortOutput))

	if len(shortOutput) != frameSize {
		t.Errorf("Short block MDCT: expected %d coefficients, got %d", frameSize, len(shortOutput))
	}

	// Verify band 10 coefficients exist
	band10Start := celt.ScaledBandStart(10, frameSize)
	band10End := celt.ScaledBandEnd(10, frameSize)
	t.Logf("Band 10 indices: %d to %d (should all be < %d)", band10Start, band10End, len(shortOutput))

	if band10End > len(shortOutput) {
		t.Errorf("Band 10 end (%d) exceeds coefficient count (%d)", band10End, len(shortOutput))
	}
}
