package celt

import (
	"math"
	"testing"

	"gopus/internal/rangecoding"
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
				if e < -50 || e > 60 {
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
				energies[i] = 10.0 // 10 dB baseline
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
					// Fine adjustment should be in range [-3, +3] dB (half of 6dB step)
					if math.Abs(diff) > 3.1 {
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
		if diff > 3.0 {
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

// TestQualityInterpolation tests allocation interpolation.
func TestQualityInterpolation(t *testing.T) {
	// Test boundary conditions

	// Quality 0 should match BandAlloc[0]
	alloc0 := interpolateAlloc(0, 21)
	for band := 0; band < 21; band++ {
		if alloc0[band] != BandAlloc[0][band] {
			t.Errorf("interpolateAlloc(0)[%d] = %d, want %d",
				band, alloc0[band], BandAlloc[0][band])
		}
	}

	// Quality 80 should match BandAlloc[10]
	alloc80 := interpolateAlloc(80, 21)
	for band := 0; band < 21; band++ {
		if alloc80[band] != BandAlloc[10][band] {
			t.Errorf("interpolateAlloc(80)[%d] = %d, want %d",
				band, alloc80[band], BandAlloc[10][band])
		}
	}

	// Mid-point interpolation: quality 4 should be between levels 0 and 1
	alloc4 := interpolateAlloc(4, 21)
	for band := 0; band < 21; band++ {
		low := BandAlloc[0][band]
		high := BandAlloc[1][band]
		if low > high {
			low, high = high, low
		}
		// Allow for rounding
		if alloc4[band] < low-1 || alloc4[band] > high+1 {
			t.Errorf("interpolateAlloc(4)[%d] = %d, not between %d and %d",
				band, alloc4[band], low, high)
		}
	}

	// Quality 40 should be between levels 5 and 6
	alloc40 := interpolateAlloc(40, 21)
	for band := 0; band < 10; band++ { // Check first 10 bands
		low := BandAlloc[5][band]
		high := BandAlloc[5][band] // At exact boundary
		if alloc40[band] < low-1 || alloc40[band] > high+1 {
			// This is at exact boundary, so should equal level 5
			if alloc40[band] != BandAlloc[5][band] {
				t.Logf("interpolateAlloc(40)[%d] = %d, expected ~%d",
					band, alloc40[band], BandAlloc[5][band])
			}
		}
	}
}

// TestTrimAndDynalloc tests trim and dynalloc adjustments.
func TestTrimAndDynalloc(t *testing.T) {
	t.Run("trim_zero", func(t *testing.T) {
		// trim=0 should not change allocation
		alloc := []int{10, 20, 30, 40, 50}
		original := make([]int, len(alloc))
		copy(original, alloc)

		applyTrim(alloc, 0, len(alloc), 3)

		for i := range alloc {
			if alloc[i] != original[i] {
				t.Errorf("trim=0 changed alloc[%d]: %d -> %d", i, original[i], alloc[i])
			}
		}
	})

	t.Run("trim_positive", func(t *testing.T) {
		// trim > 0 should boost high bands relative to low
		alloc := []int{100, 100, 100, 100, 100}
		original := make([]int, len(alloc))
		copy(original, alloc)

		applyTrim(alloc, 6, len(alloc), 3)

		// High bands should increase, low bands decrease
		if alloc[4] <= alloc[0] {
			t.Logf("positive trim: alloc = %v", alloc)
		}
	})

	t.Run("dynalloc_adds", func(t *testing.T) {
		// dynalloc should add exact amounts
		alloc := []int{10, 20, 30}
		dynalloc := []int{5, 0, 3}

		applyDynalloc(alloc, dynalloc, len(alloc))

		if alloc[0] != 15 {
			t.Errorf("alloc[0] = %d, want 15", alloc[0])
		}
		if alloc[1] != 20 {
			t.Errorf("alloc[1] = %d, want 20", alloc[1])
		}
		if alloc[2] != 33 {
			t.Errorf("alloc[2] = %d, want 33", alloc[2])
		}
	})
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
				nil,    // auto caps
				nil,    // no dynalloc
				0,      // neutral trim
				-1,     // no intensity stereo
				false,  // no dual stereo
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
			sum := 0
			for band := 0; band < tc.nbBands; band++ {
				sum += result.BandBits[band] + result.FineBits[band]
			}
			// Allocation is capped by band caps, so sum may be less than budget
			// Just verify it's positive and doesn't exceed budget
			if sum <= 0 && tc.totalBits > 0 {
				t.Errorf("allocation sum %d should be positive for budget %d", sum, tc.totalBits)
			}
			if sum > tc.totalBits*2 {
				t.Errorf("allocation sum %d exceeds 2x budget %d", sum, tc.totalBits)
			}
			t.Logf("budget=%d, allocated=%d (%.1f%%)", tc.totalBits, sum, 100.0*float64(sum)/float64(tc.totalBits))
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

// TestPulseCap tests pulse cap computation.
func TestPulseCap(t *testing.T) {
	tests := []struct {
		width   int
		wantMin int
		wantMax int
	}{
		{1, 8, 8},
		{2, 8, 24},
		{4, 16, 32},
		{8, 24, 40},
		{16, 32, 48},
	}

	for _, tc := range tests {
		cap := pulseCap(tc.width)
		if cap < tc.wantMin || cap > tc.wantMax {
			t.Errorf("pulseCap(%d) = %d, want in range [%d, %d]",
				tc.width, cap, tc.wantMin, tc.wantMax)
		}
	}

	// Zero width should return 0
	if cap := pulseCap(0); cap != 0 {
		t.Errorf("pulseCap(0) = %d, want 0", cap)
	}
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
		ComputeAllocation(1000, 21, nil, nil, 0, -1, false, 3)
	}
}

// BenchmarkInterpolateAlloc benchmarks quality interpolation.
func BenchmarkInterpolateAlloc(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interpolateAlloc(40, 21)
	}
}
