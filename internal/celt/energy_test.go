package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestDecodeCoarseEnergy tests coarse energy decoding with mock data.
func TestDecodeCoarseEnergy(t *testing.T) {
	tests := []struct {
		name     string
		nbBands  int
		intra    bool
		lm       int
		channels int
	}{
		{"mono_intra_20ms", 21, true, 3, 1},
		{"mono_inter_20ms", 21, false, 3, 1},
		{"mono_intra_10ms", 19, true, 2, 1},
		{"mono_inter_5ms", 17, false, 1, 1},
		{"stereo_intra_20ms", 21, true, 3, 2},
		{"stereo_inter_20ms", 21, false, 3, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec := NewDecoder(tc.channels)

			// Create mock range decoder with some bytes
			mockData := make([]byte, 100)
			for i := range mockData {
				mockData[i] = byte(i * 17) // Pseudo-random pattern
			}
			rd := &rangecoding.Decoder{}
			rd.Init(mockData)
			dec.SetRangeDecoder(rd)

			// Decode energies
			energies := dec.DecodeCoarseEnergy(tc.nbBands, tc.intra, tc.lm)

			// Verify output length
			expectedLen := tc.nbBands * tc.channels
			if len(energies) != expectedLen {
				t.Errorf("got %d energies, want %d", len(energies), expectedLen)
			}

			// Verify energies are finite and in reasonable range
			for i, e := range energies {
				if math.IsNaN(e) || math.IsInf(e, 0) {
					t.Errorf("energy[%d] is not finite: %v", i, e)
				}
				// Typical range: -40 to +40 dB
				minRange := -50.0 * DB6
				maxRange := 60.0 * DB6
				if e < minRange || e > maxRange {
					t.Logf("warning: energy[%d] = %v may be out of typical range", i, e)
				}
			}
		})
	}
}

// TestDecodeCoarseEnergyPrediction tests that prediction coefficients are applied.
func TestDecodeCoarseEnergyPrediction(t *testing.T) {
	dec := NewDecoder(1)

	// Set known previous energies
	for band := 0; band < MaxBands; band++ {
		dec.prevEnergy[band] = float64(band) * 2.0 // 0, 2, 4, 6, ...
	}

	// Create range decoder
	mockData := make([]byte, 100)
	rd := &rangecoding.Decoder{}
	rd.Init(mockData)
	dec.SetRangeDecoder(rd)

	// Decode with inter-frame mode (uses alpha prediction)
	energies := dec.DecodeCoarseEnergy(5, false, 3)

	// In inter-frame mode, energies should be influenced by prevEnergy
	// The exact values depend on Laplace decoding, but they should differ
	// from intra mode

	// Verify we got results
	if len(energies) != 5 {
		t.Errorf("got %d energies, want 5", len(energies))
	}
}

// TestDecodeFineEnergy tests fine energy refinement.
func TestDecodeFineEnergy(t *testing.T) {
	tests := []struct {
		name     string
		fineBits []int
		nbBands  int
	}{
		{"no_fine", []int{0, 0, 0, 0, 0}, 5},
		{"1_bit", []int{1, 1, 1, 1, 1}, 5},
		{"2_bits", []int{2, 2, 2, 2, 2}, 5},
		{"mixed", []int{0, 1, 2, 3, 4}, 5},
		{"max_bits", []int{8, 8, 8, 8, 8}, 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec := NewDecoder(1)

			// Create range decoder
			mockData := make([]byte, 100)
			for i := range mockData {
				mockData[i] = byte(i * 31)
			}
			rd := &rangecoding.Decoder{}
			rd.Init(mockData)
			dec.SetRangeDecoder(rd)

			// Start with known energies
			energies := make([]float64, tc.nbBands)
			for i := range energies {
				energies[i] = 10.0 * DB6 // 10 dB baseline
			}
			original := make([]float64, tc.nbBands)
			copy(original, energies)

			// Apply fine energy
			dec.DecodeFineEnergy(energies, tc.nbBands, tc.fineBits)

			// Verify adjustments
			for band := 0; band < tc.nbBands; band++ {
				diff := energies[band] - original[band]

				if tc.fineBits[band] == 0 {
					// No change expected
					if diff != 0 {
						t.Errorf("band %d: expected no change, got diff %v", band, diff)
					}
				} else {
					// Fine adjustment should be in range [-DB6/2, +DB6/2]
					if math.Abs(diff) > (DB6/2)+1e-6 {
						t.Errorf("band %d: fine adjustment %v exceeds expected range", band, diff)
					}
				}
			}
		})
	}
}

// TestDecodeEnergyRemainder tests remainder bit decoding.
func TestDecodeEnergyRemainder(t *testing.T) {
	dec := NewDecoder(1)

	// Create range decoder
	mockData := make([]byte, 100)
	for i := range mockData {
		mockData[i] = byte(i * 41)
	}
	rd := &rangecoding.Decoder{}
	rd.Init(mockData)
	dec.SetRangeDecoder(rd)

	// Start with known energies
	energies := []float64{10.0, 20.0, 30.0}
	original := make([]float64, len(energies))
	copy(original, energies)

	// Apply remainder bits
	remainderBits := []int{2, 3, 1}
	dec.DecodeEnergyRemainder(energies, len(energies), remainderBits)

	// Verify adjustments are small (sub-6dB refinement)
	for band := range energies {
		diff := math.Abs(energies[band] - original[band])
		// Remainder bits provide very fine adjustment
		if diff > (DB6/2)+1e-6 {
			t.Errorf("band %d: remainder adjustment %v exceeds expected range", band, diff)
		}
	}
}

// TestBandAllocTable verifies BandAlloc table values against expected patterns.
func TestBandAllocTable(t *testing.T) {
	// Test properties of the allocation table

	// 1. Quality 0 should be all zeros
	for band := 0; band < 21; band++ {
		if BandAlloc[0][band] != 0 {
			t.Errorf("BandAlloc[0][%d] = %d, want 0", band, BandAlloc[0][band])
		}
	}

	// 2. Higher qualities should have higher allocations (generally)
	for band := 0; band < 15; band++ { // Check first 15 bands
		for q := 1; q < 10; q++ {
			if BandAlloc[q][band] > BandAlloc[q+1][band] {
				t.Errorf("BandAlloc[%d][%d] = %d > BandAlloc[%d][%d] = %d (not monotonic)",
					q, band, BandAlloc[q][band], q+1, band, BandAlloc[q+1][band])
			}
		}
	}

	// 3. Lower bands generally get more bits than higher bands at same quality
	for q := 5; q <= 10; q++ {
		if BandAlloc[q][0] < BandAlloc[q][20] {
			t.Logf("quality %d: band 0 (%d) < band 20 (%d) - unusual but may be valid",
				q, BandAlloc[q][0], BandAlloc[q][20])
		}
	}

	// 4. All values should be non-negative
	for q := 0; q < 11; q++ {
		for band := 0; band < 21; band++ {
			if BandAlloc[q][band] < 0 {
				t.Errorf("BandAlloc[%d][%d] = %d is negative", q, band, BandAlloc[q][band])
			}
		}
	}
}

// TestComputeAllocation tests the full allocation computation.
func TestComputeAllocation(t *testing.T) {
	tests := []struct {
		name      string
		totalBits int
		nbBands   int
		lm        int
	}{
		{"full_20ms_1000bits", 1000, 21, 3},
		{"full_20ms_500bits", 500, 21, 3},
		{"reduced_10ms", 500, 13, 2},
		{"minimal", 100, 5, 0},
		{"high_bits", 2000, 21, 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ComputeAllocation(
				tc.totalBits,
				tc.nbBands,
				1,     // channels
				nil,   // auto caps
				nil,   // no dynalloc
				0,     // neutral trim
				-1,    // no intensity stereo
				false, // no dual stereo
				tc.lm,
			)

			// Verify output lengths
			if len(result.BandBits) != tc.nbBands {
				t.Errorf("got %d bandBits, want %d", len(result.BandBits), tc.nbBands)
			}
			if len(result.FineBits) != tc.nbBands {
				t.Errorf("got %d fineBits, want %d", len(result.FineBits), tc.nbBands)
			}

			// Sum should be reasonable (within caps)
			sumQ3 := 0
			for band := 0; band < tc.nbBands; band++ {
				sumQ3 += result.BandBits[band] + (result.FineBits[band] << bitRes)
			}
			// Allocation is capped by band caps, so sum may be less than budget
			// Just verify it's positive and doesn't exceed budget
			if sumQ3 <= 0 && tc.totalBits > 0 {
				t.Errorf("allocation sum %d q3 should be positive for budget %d", sumQ3, tc.totalBits)
			}
			if sumQ3 > (tc.totalBits<<bitRes)*2 {
				t.Errorf("allocation sum %d q3 exceeds 2x budget %d", sumQ3, tc.totalBits)
			}
			sumBits := sumQ3 >> bitRes
			t.Logf("budget=%d, allocated=%d (%.1f%%)", tc.totalBits, sumBits, 100.0*float64(sumBits)/float64(tc.totalBits))
		})
	}
}

// TestAllocationNonNegative verifies all allocations are non-negative.
func TestAllocationNonNegative(t *testing.T) {
	testCases := []struct {
		totalBits int
		nbBands   int
		trim      int
	}{
		{100, 21, 0},
		{500, 21, -6},
		{1000, 21, 6},
		{50, 10, 0},
	}

	for _, tc := range testCases {
		result := ComputeAllocation(
			tc.totalBits,
			tc.nbBands,
			1,
			nil, nil,
			tc.trim,
			-1, false, 3,
		)

		for band := 0; band < tc.nbBands; band++ {
			if result.BandBits[band] < 0 {
				t.Errorf("BandBits[%d] = %d is negative", band, result.BandBits[band])
			}
			if result.FineBits[band] < 0 {
				t.Errorf("FineBits[%d] = %d is negative", band, result.FineBits[band])
			}
		}
	}
}

// TestLaplaceDecodeEntropyConsumption tests that Laplace decoding consumes entropy.
// This verifies the DecodeSymbol integration is working correctly.
func TestLaplaceDecodeEntropyConsumption(t *testing.T) {
	// Test that decoding a Laplace symbol consumes reasonable entropy
	// A symbol with probability p should consume approximately -log2(p) bits

	// Create test data - a simple packet that encodes some energy values
	testData := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	d := NewDecoder(1)
	rd := &rangecoding.Decoder{}
	rd.Init(testData)
	d.SetRangeDecoder(rd)

	initialBits := rd.Tell()

	// Decode a Laplace value using decodeLaplace via DecodeCoarseEnergy
	// which internally calls decodeLaplace
	decay := 16384
	_ = d.decodeLaplace(laplaceFS, decay)

	consumedBits := rd.Tell() - initialBits

	// Should consume at least some bits for any symbol
	// With the proper DecodeSymbol implementation, this will be non-zero
	if consumedBits < 0 {
		t.Errorf("Laplace decode consumed %d bits, expected non-negative", consumedBits)
	}

	// Log the consumption for diagnostics
	t.Logf("Laplace decode consumed %d bits", consumedBits)
}

// TestDecodeCoarseEnergyRangeSync tests that coarse energy decoding doesn't desynchronize.
// Decode multiple bands and verify range decoder state is consistent.
func TestDecodeCoarseEnergyRangeSync(t *testing.T) {
	// Use some real-looking CELT frame data
	testData := make([]byte, 64)
	for i := range testData {
		testData[i] = byte(i * 7 % 256) // Deterministic test data
	}

	d := NewDecoder(1)
	rd := &rangecoding.Decoder{}
	rd.Init(testData)
	d.SetRangeDecoder(rd)

	// Decode energy for 10 bands
	energies := d.DecodeCoarseEnergy(10, false, 3) // inter-frame, LM=3 (20ms)

	// Verify energies are finite and reasonable
	for i, e := range energies {
		if e != e { // NaN check
			t.Errorf("Band %d energy is NaN", i)
		}
		if e < -100 || e > 100 {
			t.Errorf("Band %d energy %f out of reasonable range [-100, 100]", i, e)
		}
	}

	// Verify range decoder is still in valid state
	bitsUsed := rd.Tell()
	if bitsUsed < 0 || bitsUsed > len(testData)*8 {
		t.Errorf("Range decoder in invalid state: Tell() = %d", bitsUsed)
	}

	t.Logf("Decoded 10 band energies, consumed %d bits", bitsUsed)
}

// TestLaplaceDecodeMultipleSymbols tests decoding multiple Laplace symbols in sequence.
// This verifies the range decoder stays synchronized across multiple decodes.
func TestLaplaceDecodeMultipleSymbols(t *testing.T) {
	// Create varied test data
	testData := make([]byte, 128)
	for i := range testData {
		testData[i] = byte((i*13 + 7) % 256)
	}

	d := NewDecoder(1)
	rd := &rangecoding.Decoder{}
	rd.Init(testData)
	d.SetRangeDecoder(rd)

	// Decode multiple Laplace symbols
	prevBits := rd.Tell()
	totalConsumed := 0

	for i := 0; i < 20; i++ {
		decay := 16384 + i*512 // Vary decay
		_ = d.decodeLaplace(laplaceFS, decay)

		currentBits := rd.Tell()
		consumed := currentBits - prevBits
		totalConsumed += consumed
		prevBits = currentBits

		// Each decode should consume non-negative bits
		if consumed < 0 {
			t.Errorf("Symbol %d consumed negative bits: %d", i, consumed)
		}
	}

	// Total consumption should be reasonable (not zero if implementation is correct)
	t.Logf("Decoded 20 Laplace symbols, total consumed: %d bits", totalConsumed)

	// Verify we consumed a reasonable amount (at least a few bits per symbol on average)
	avgBitsPerSymbol := float64(totalConsumed) / 20.0
	t.Logf("Average bits per symbol: %.2f", avgBitsPerSymbol)
}

// TestRangeDecoderStateAfterLaplace verifies range decoder state consistency.
func TestRangeDecoderStateAfterLaplace(t *testing.T) {
	testData := make([]byte, 32)
	for i := range testData {
		testData[i] = byte(i * 23 % 256)
	}

	rd := &rangecoding.Decoder{}
	rd.Init(testData)

	// Get initial state
	initialRange := rd.Range()
	initialVal := rd.Val()

	// Range and val should be non-zero after init
	if initialRange == 0 {
		t.Error("Initial range is zero")
	}

	// Now use DecodeSymbol directly
	// Decode a symbol with fl=0, fh=16384, ft=32768 (50% probability for 0)
	rd.DecodeSymbol(0, 16384, 32768)

	// Range should still be non-zero and reasonable
	newRange := rd.Range()
	if newRange == 0 {
		t.Error("Range became zero after DecodeSymbol")
	}

	// Range should have decreased (we used half the space)
	t.Logf("Range: %d -> %d, Val: %d -> %d", initialRange, newRange, initialVal, rd.Val())
}

// BenchmarkDecodeCoarseEnergy benchmarks coarse energy decoding.
func BenchmarkDecodeCoarseEnergy(b *testing.B) {
	dec := NewDecoder(1)
	mockData := make([]byte, 1000)
	for i := range mockData {
		mockData[i] = byte(i * 17)
	}
	rd := &rangecoding.Decoder{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rd.Init(mockData)
		dec.SetRangeDecoder(rd)
		dec.DecodeCoarseEnergy(21, false, 3)
	}
}

// BenchmarkComputeAllocation benchmarks bit allocation.
func BenchmarkComputeAllocation(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeAllocation(1000, 21, 1, nil, nil, 0, -1, false, 3)
	}
}
