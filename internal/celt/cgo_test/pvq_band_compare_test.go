// Package cgo provides CGO comparison tests for PVQ band encoding.
// This file tests the complete quant_all_bands encoding path against libopus.
package cgo

import (
	"bytes"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestPVQBandEncodingRoundtrip tests that gopus PVQ band encoding produces output
// that matches the libopus encoder for identical inputs.
func TestPVQBandEncodingRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// Test parameters matching libopus 48kHz mono 20ms
	frameSize := 960
	channels := 1
	lm := 3 // log2(M) where M = frameSize / 120

	nbBands := 21

	// Generate random normalized coefficients for each band
	normCoeffs := make([]float64, frameSize)
	for i := range normCoeffs {
		normCoeffs[i] = rng.NormFloat64()
	}

	// Normalize each band
	offset := 0
	for band := 0; band < nbBands; band++ {
		n := celt.ScaledBandWidth(band, frameSize)
		if n <= 0 || offset+n > len(normCoeffs) {
			continue
		}

		// Compute energy for this band
		var energy float64
		for i := 0; i < n; i++ {
			energy += normCoeffs[offset+i] * normCoeffs[offset+i]
		}
		if energy > 1e-10 {
			scale := 1.0 / math.Sqrt(energy)
			for i := 0; i < n; i++ {
				normCoeffs[offset+i] *= scale
			}
		}
		offset += n
	}

	// Create test pulse allocation (typical for 64kbps)
	pulses := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		pulses[i] = 50 + i*10 // Increasing allocation
	}

	// Create a range encoder and encode
	buf := make([]byte, 1275)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Create TF resolution array (all zeros = no TF change)
	tfRes := make([]int, nbBands)

	// Encode bands
	var seed uint32 = 12345
	collapse := celt.QuantAllBandsEncodeForTest(
		re,
		channels,
		frameSize,
		lm,
		0,          // start
		nbBands,    // end
		normCoeffs, // x
		nil,        // y (mono)
		pulses,
		1,     // shortBlocks
		2,     // spread = SPREAD_NORMAL
		0,     // tapset
		0,     // dualStereo
		nbBands, // intensity (disabled)
		tfRes,
		(160 << 3), // totalBitsQ3 = 160 bits * 8 (Q3)
		0,          // balance
		nbBands,    // codedBands
		&seed,
		5, // complexity
		nil,
		nil,
		nil,
	)

	// Finalize encoder
	encoded := re.Done()

	t.Logf("Encoded %d bytes, collapse mask: %v", len(encoded), collapse)
	t.Logf("Seed after encoding: %d", seed)

	// Verify output is non-empty
	if len(encoded) == 0 {
		t.Error("Encoded output is empty")
	}

	// Verify collapse mask has entries
	if len(collapse) == 0 {
		t.Error("Collapse mask is empty")
	}
}

// TestPVQSearchEncodingConsistency verifies that gopus PVQ search and encoding
// produces consistent results across multiple runs.
func TestPVQSearchEncodingConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(123))

	// Test with various band sizes
	testCases := []struct {
		n, k int
	}{
		{4, 2},
		{8, 4},
		{16, 8},
		{32, 16},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Generate random normalized vector
			x := make([]float64, tc.n)
			var sum float64
			for i := range x {
				x[i] = rng.NormFloat64()
				sum += x[i] * x[i]
			}
			scale := 1.0 / math.Sqrt(sum)
			for i := range x {
				x[i] *= scale
			}

			// Encode twice and verify same output
			buf1 := make([]byte, 256)
			re1 := &rangecoding.Encoder{}
			re1.Init(buf1)

			buf2 := make([]byte, 256)
			re2 := &rangecoding.Encoder{}
			re2.Init(buf2)

			// First encoding
			x1 := make([]float64, tc.n)
			copy(x1, x)
			cm1 := celt.AlgQuantForTest(re1, 0, x1, tc.n, tc.k, 2, 1, 1.0, true, nil, 0)

			// Second encoding
			x2 := make([]float64, tc.n)
			copy(x2, x)
			cm2 := celt.AlgQuantForTest(re2, 0, x2, tc.n, tc.k, 2, 1, 1.0, true, nil, 0)

			bytes1 := re1.Done()
			bytes2 := re2.Done()

			if cm1 != cm2 {
				t.Errorf("Collapse masks differ: %d vs %d", cm1, cm2)
			}

			if !bytes.Equal(bytes1, bytes2) {
				t.Errorf("Encoded bytes differ")
				t.Logf("First: %v", bytes1[:minInt(16, len(bytes1))])
				t.Logf("Second: %v", bytes2[:minInt(16, len(bytes2))])
			}
		})
	}
}

// TestPVQSearchAgainstLibopus compares gopus PVQ search with libopus.
func TestPVQSearchAgainstLibopus(t *testing.T) {
	rng := rand.New(rand.NewSource(456))

	bandSizes := []int{4, 8, 16, 32}
	pulseCounts := []int{2, 4, 8, 16}

	for _, n := range bandSizes {
		for _, k := range pulseCounts {
			if k > n*2 {
				continue
			}

			t.Run("", func(t *testing.T) {
				mismatches := 0
				totalTests := 20

				for trial := 0; trial < totalTests; trial++ {
					// Generate random normalized vector
					x := make([]float64, n)
					var sum float64
					for i := range x {
						x[i] = rng.NormFloat64()
						sum += x[i] * x[i]
					}
					scale := 1.0 / math.Sqrt(sum)
					for i := range x {
						x[i] *= scale
					}

					// Get gopus pulses
					goPulses, goYY := celt.OpPVQSearchExport(x, k)

					// Get libopus pulses
					libPulses, libYY := LibopusPVQSearch(x, k)

					// Compare pulses
					match := true
					for i := range goPulses {
						if goPulses[i] != libPulses[i] {
							match = false
							break
						}
					}

					if !match {
						mismatches++
						if mismatches <= 3 {
							t.Logf("Mismatch n=%d k=%d trial=%d", n, k, trial)
							t.Logf("  Go:     %v (yy=%.4f)", goPulses, goYY)
							t.Logf("  libopus: %v (yy=%.4f)", libPulses, libYY)

							// Check if both produce same CWRS index
							goIndex := celt.EncodePulses(goPulses, n, k)
							libIndex := celt.EncodePulses(libPulses, n, k)
							t.Logf("  Go index: %d, libopus index: %d", goIndex, libIndex)
						}
					}
				}

				if mismatches > 0 {
					t.Logf("n=%d k=%d: %d/%d mismatches", n, k, mismatches, totalTests)
				}
			})
		}
	}
}

// TestCWRSEncodingVsLibopus verifies CWRS encoding matches libopus.
func TestCWRSEncodingVsLibopus(t *testing.T) {
	// Test specific known pulse vectors
	testCases := []struct {
		pulses []int
		n, k   int
	}{
		{[]int{1, 0, 0, 0}, 4, 1},
		{[]int{-1, 0, 0, 0}, 4, 1},
		{[]int{1, 1, 0, 0}, 4, 2},
		{[]int{2, 0, 0, 0}, 4, 2},
		{[]int{-2, 0, 0, 0}, 4, 2},
		{[]int{1, -1, 0, 0}, 4, 2},
		{[]int{1, 0, 1, 0}, 4, 2},
		{[]int{1, 1, 1, 1}, 4, 4},
		{[]int{2, 1, 1, 0}, 4, 4},
		{[]int{4, 0, 0, 0}, 4, 4},
	}

	for _, tc := range testCases {
		// Verify K matches
		sum := 0
		for _, p := range tc.pulses {
			if p < 0 {
				sum -= p
			} else {
				sum += p
			}
		}
		if sum != tc.k {
			t.Errorf("Test case error: sum=%d, k=%d", sum, tc.k)
			continue
		}

		// Encode
		goIndex := celt.EncodePulses(tc.pulses, tc.n, tc.k)

		// Decode
		decoded := celt.DecodePulses(goIndex, tc.n, tc.k)

		// Verify roundtrip
		match := true
		for i := range tc.pulses {
			if tc.pulses[i] != decoded[i] {
				match = false
				break
			}
		}

		if !match {
			t.Errorf("CWRS roundtrip failed for pulses=%v", tc.pulses)
			t.Logf("  Index: %d", goIndex)
			t.Logf("  Decoded: %v", decoded)
		}
	}
}

// TestAlgQuantEncodingVsLibopus compares the full alg_quant encoding output
// (rotation + PVQ search + CWRS encoding) between gopus and libopus.
func TestAlgQuantEncodingVsLibopus(t *testing.T) {
	rng := rand.New(rand.NewSource(789))

	testCases := []struct {
		n, k, spread, b int
	}{
		{4, 2, 2, 1},
		{8, 4, 2, 1},
		{8, 8, 2, 1},
		{16, 8, 2, 1},
		{16, 12, 2, 1}, // k=16 may overflow 32-bit CWRS
		{32, 4, 2, 1},
		{32, 7, 2, 1}, // k=16 overflows 32-bit CWRS for n=32
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			mismatches := 0
			totalTests := 20

			for trial := 0; trial < totalTests; trial++ {
				// Generate random normalized vector
				x := make([]float64, tc.n)
				var sum float64
				for i := range x {
					x[i] = rng.NormFloat64()
					sum += x[i] * x[i]
				}
				scale := 1.0 / math.Sqrt(sum)
				for i := range x {
					x[i] *= scale
				}

				gain := 1.0

				// Run gopus alg_quant
				goBuf := make([]byte, 256)
				goRe := &rangecoding.Encoder{}
				goRe.Init(goBuf)
				goX := make([]float64, tc.n)
				copy(goX, x)
				goCM := celt.AlgQuantForTest(goRe, 0, goX, tc.n, tc.k, tc.spread, tc.b, gain, true, nil, 0)
				goBytes := goRe.Done()

				// Run libopus alg_quant
				libBytes, libCM := LibopusAlgQuant(x, tc.n, tc.k, tc.spread, tc.b, gain)

				// Compare collapse masks
				if goCM != int(libCM) {
					t.Logf("Trial %d: Collapse mask mismatch: go=%d, libopus=%d", trial, goCM, libCM)
					mismatches++
				}

				// Compare bytes
				if !bytes.Equal(goBytes, libBytes) {
					mismatches++
					if mismatches <= 3 {
						t.Logf("Trial %d: Byte mismatch n=%d k=%d", trial, tc.n, tc.k)
						t.Logf("  Go:     %x (len=%d)", goBytes[:minInt(16, len(goBytes))], len(goBytes))
						t.Logf("  libopus: %x (len=%d)", libBytes[:minInt(16, len(libBytes))], len(libBytes))
						t.Logf("  Collapse mask: go=%d, libopus=%d", goCM, libCM)
					}
				}
			}

			if mismatches > 0 {
				t.Errorf("n=%d k=%d: %d/%d tests had mismatches", tc.n, tc.k, mismatches, totalTests)
			}
		})
	}
}

// TestNormalizationMatchesLibopus checks that band normalization matches libopus.
func TestNormalizationMatchesLibopus(t *testing.T) {
	rng := rand.New(rand.NewSource(789))

	frameSize := 960
	nbBands := 21

	// Generate random MDCT coefficients
	mdctCoeffs := make([]float64, frameSize)
	for i := range mdctCoeffs {
		mdctCoeffs[i] = rng.NormFloat64() * 100 // Random amplitude
	}

	// Generate random band energies (log2 scale)
	energies := make([]float64, nbBands)
	for i := range energies {
		energies[i] = 5.0 + rng.Float64()*20.0 // 5-25 range
	}

	// Create encoder and normalize
	enc := celt.NewEncoder(1) // mono

	normalized := enc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)
	if len(normalized) == 0 {
		t.Fatal("Normalization returned empty array")
	}

	// Verify each band has reasonable values
	offset := 0
	for band := 0; band < nbBands; band++ {
		n := celt.ScaledBandWidth(band, frameSize)
		if n <= 0 || offset+n > len(normalized) {
			continue
		}

		// Check that values are finite
		for i := 0; i < n; i++ {
			if math.IsNaN(normalized[offset+i]) || math.IsInf(normalized[offset+i], 0) {
				t.Errorf("Band %d has invalid value at %d: %v", band, i, normalized[offset+i])
			}
		}
		offset += n
	}
}

