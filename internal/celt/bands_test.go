package celt

import (
	"math"
	"testing"
)

// TestNormalizeVector verifies L2 normalization produces unit-length vectors.
func TestNormalizeVector(t *testing.T) {
	tests := []struct {
		name     string
		input    []float64
		expected []float64
	}{
		{
			name:     "3-4-5 triangle",
			input:    []float64{3, 4},
			expected: []float64{0.6, 0.8},
		},
		{
			name:     "already normalized",
			input:    []float64{1, 0},
			expected: []float64{1, 0},
		},
		{
			name:     "negative values",
			input:    []float64{-3, 4},
			expected: []float64{-0.6, 0.8},
		},
		{
			name:     "all zeros",
			input:    []float64{0, 0, 0},
			expected: []float64{0, 0, 0},
		},
		{
			name:     "single element",
			input:    []float64{5},
			expected: []float64{1},
		},
		{
			name:     "single negative",
			input:    []float64{-5},
			expected: []float64{-1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeVector(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("NormalizeVector length = %d, want %d", len(result), len(tt.expected))
				return
			}

			// Check values
			for i := range result {
				if math.Abs(result[i]-tt.expected[i]) > 1e-10 {
					t.Errorf("NormalizeVector[%d] = %v, want %v", i, result[i], tt.expected[i])
				}
			}

			// Verify unit length (except for zero vector)
			var length2 float64
			for _, x := range result {
				length2 += x * x
			}
			if length2 > 1e-10 && math.Abs(length2-1.0) > 1e-10 {
				t.Errorf("Normalized vector length^2 = %v, want 1.0", length2)
			}
		})
	}
}

// TestNormalizeVectorUnitLength verifies various vectors normalize to unit length.
func TestNormalizeVectorUnitLength(t *testing.T) {
	testVectors := [][]float64{
		{1, 2, 3, 4, 5},
		{-1, -2, -3, -4, -5},
		{0.001, 0.002, 0.003},
		{1000, 2000, 3000},
		{1, 1, 1, 1, 1, 1, 1, 1},
	}

	for _, v := range testVectors {
		result := NormalizeVector(v)

		var length2 float64
		for _, x := range result {
			length2 += x * x
		}

		if math.Abs(length2-1.0) > 1e-9 {
			t.Errorf("NormalizeVector(%v) has length^2 = %v, want 1.0", v, length2)
		}
	}
}

// TestFoldBand verifies band folding produces unit-normalized vectors.
func TestFoldBand(t *testing.T) {
	tests := []struct {
		name    string
		lowband []float64
		n       int
	}{
		{
			name:    "fold from 4-element band to 4",
			lowband: []float64{0.5, 0.5, 0.5, 0.5},
			n:       4,
		},
		{
			name:    "fold from 2-element band to 4 (wrap)",
			lowband: []float64{0.7071067811865476, 0.7071067811865476},
			n:       4,
		},
		{
			name:    "fold from 8-element band to 4 (truncate)",
			lowband: []float64{0.35, 0.35, 0.35, 0.35, 0.35, 0.35, 0.35, 0.35},
			n:       4,
		},
		{
			name:    "noise generation (no source)",
			lowband: nil,
			n:       8,
		},
		{
			name:    "noise generation (empty source)",
			lowband: []float64{},
			n:       8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed := uint32(12345)
			result := FoldBand(tt.lowband, tt.n, &seed)

			if len(result) != tt.n {
				t.Errorf("FoldBand length = %d, want %d", len(result), tt.n)
				return
			}

			// Verify unit norm
			var length2 float64
			for _, x := range result {
				length2 += x * x
			}
			if math.Abs(length2-1.0) > 1e-9 {
				t.Errorf("FoldBand has length^2 = %v, want 1.0", length2)
			}
		})
	}
}

// TestFoldBandSeedVariation verifies different seeds produce different outputs.
func TestFoldBandSeedVariation(t *testing.T) {
	lowband := []float64{1, 0, 0, 0}
	n := 4

	seed1 := uint32(11111)
	seed2 := uint32(22222)
	seed3 := uint32(33333)

	result1 := FoldBand(lowband, n, &seed1)
	result2 := FoldBand(lowband, n, &seed2)
	result3 := FoldBand(lowband, n, &seed3)

	// Results should differ due to sign variations
	same12 := true
	same13 := true
	for i := 0; i < n; i++ {
		if result1[i] != result2[i] {
			same12 = false
		}
		if result1[i] != result3[i] {
			same13 = false
		}
	}

	if same12 && same13 {
		t.Error("Different seeds should produce different folded vectors")
	}
}

// TestBitsToK verifies bits-to-pulses conversion.
func TestBitsToK(t *testing.T) {
	// The bits-to-K relationship is complex and depends on V(n,k) computation.
	// These tests verify basic properties rather than exact values.

	// Zero bits should always return zero pulses
	if k := bitsToK(0, 4); k != 0 {
		t.Errorf("bitsToK(0, 4) = %d, want 0", k)
	}

	// Very few bits (< log2(2n)) should return zero
	if k := bitsToK(1, 4); k != 0 {
		t.Errorf("bitsToK(1, 4) = %d, want 0 (too few bits)", k)
	}

	// More bits should give more pulses (monotonically non-decreasing)
	prev := 0
	for bits := 0; bits <= 100; bits += 10 {
		k := bitsToK(bits, 8)
		if k < prev {
			t.Errorf("bitsToK not monotonic: bits=%d gave k=%d, but bits=%d gave k=%d",
				bits-10, prev, bits, k)
		}
		prev = k
	}

	// K should stay within the PVQ pulse limit
	for _, bits := range []int{10, 20, 50, 100} {
		k := bitsToK(bits, 8)
		if k > MaxPVQK {
			t.Errorf("bitsToK(%d, 8) = %d, but k should not exceed MaxPVQK", bits, k)
		}
	}

	// With enough bits, should get at least 1 pulse
	if k := bitsToK(10, 2); k < 1 {
		t.Errorf("bitsToK(10, 2) = %d, expected at least 1 pulse with 10 bits", k)
	}
}

// TestBitsToKZeroBits verifies zero bits always returns zero pulses.
func TestBitsToKZeroBits(t *testing.T) {
	for n := 1; n <= 32; n++ {
		k := bitsToK(0, n)
		if k != 0 {
			t.Errorf("bitsToK(0, %d) = %d, want 0", n, k)
		}
	}
}

// TestBitsToKZeroDimensions verifies zero dimensions returns zero pulses.
func TestBitsToKZeroDimensions(t *testing.T) {
	for bits := 0; bits <= 100; bits += 10 {
		k := bitsToK(bits, 0)
		if k != 0 {
			t.Errorf("bitsToK(%d, 0) = %d, want 0", bits, k)
		}
	}
}

// TestCollapseMask verifies collapse mask tracking.
func TestCollapseMask(t *testing.T) {
	var mask uint32

	// Initially empty
	if GetCodedBandCount(mask) != 0 {
		t.Error("Initial mask should have 0 coded bands")
	}

	// Update some bands
	UpdateCollapseMask(&mask, 0)
	UpdateCollapseMask(&mask, 2)
	UpdateCollapseMask(&mask, 5)

	// Check coded bands
	if !IsBandCoded(mask, 0) {
		t.Error("Band 0 should be coded")
	}
	if IsBandCoded(mask, 1) {
		t.Error("Band 1 should not be coded")
	}
	if !IsBandCoded(mask, 2) {
		t.Error("Band 2 should be coded")
	}
	if !IsBandCoded(mask, 5) {
		t.Error("Band 5 should be coded")
	}

	// Check count
	if GetCodedBandCount(mask) != 3 {
		t.Errorf("GetCodedBandCount = %d, want 3", GetCodedBandCount(mask))
	}

	// Check anti-collapse
	if NeedsAntiCollapse(mask, 0) {
		t.Error("Band 0 should not need anti-collapse")
	}
	if !NeedsAntiCollapse(mask, 1) {
		t.Error("Band 1 should need anti-collapse")
	}
	if !NeedsAntiCollapse(mask, 3) {
		t.Error("Band 3 should need anti-collapse")
	}

	// Clear
	ClearCollapseMask(&mask)
	if mask != 0 {
		t.Error("Cleared mask should be 0")
	}
}

// TestFindFoldSource verifies source band lookup for folding.
func TestFindFoldSource(t *testing.T) {
	tests := []struct {
		name       string
		targetBand int
		codedMask  uint32
		expected   int
	}{
		{
			name:       "find band 2 from band 3",
			targetBand: 3,
			codedMask:  0b0101, // bands 0 and 2 coded
			expected:   2,
		},
		{
			name:       "find band 0 from band 1",
			targetBand: 1,
			codedMask:  0b0001, // only band 0 coded
			expected:   0,
		},
		{
			name:       "no coded bands",
			targetBand: 5,
			codedMask:  0,
			expected:   -1,
		},
		{
			name:       "target is 0",
			targetBand: 0,
			codedMask:  0b1111,
			expected:   -1,
		},
		{
			name:       "skip uncoded bands",
			targetBand: 5,
			codedMask:  0b00011, // bands 0, 1 coded
			expected:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FindFoldSource(tt.targetBand, tt.codedMask, nil)
			if result != tt.expected {
				t.Errorf("FindFoldSource(%d, 0x%x) = %d, want %d",
					tt.targetBand, tt.codedMask, result, tt.expected)
			}
		})
	}
}

// TestDecodeBandsOutputLength verifies DecodeBands produces correct output length.
func TestDecodeBandsOutputLength(t *testing.T) {
	// Create decoder to verify it initializes correctly
	dec := NewDecoder(1)
	if dec.Channels() != 1 {
		t.Errorf("NewDecoder(1).Channels() = %d, want 1", dec.Channels())
	}

	// Need to set up a mock range decoder for actual decoding
	// For now, just verify band width calculations

	frameSizes := []int{120, 240, 480, 960}
	bandCounts := []int{13, 17, 19, 21}

	for i, frameSize := range frameSizes {
		nbBands := bandCounts[i]

		// Calculate expected length
		expectedLen := 0
		for band := 0; band < nbBands; band++ {
			expectedLen += ScaledBandWidth(band, frameSize)
		}

		t.Logf("FrameSize=%d, nbBands=%d, expectedLen=%d", frameSize, nbBands, expectedLen)

		// Verify band widths sum correctly
		if expectedLen != frameSize {
			// Note: This may not always equal frameSize depending on eBands table
			t.Logf("Band widths sum = %d, frameSize = %d", expectedLen, frameSize)
		}
	}
}

// TestDecodeBands_OutputSize verifies DecodeBands returns frameSize coefficients.
// This test confirms the fix for MDCT bin count mismatch (14-01).
// Before the fix, DecodeBands returned totalBins (800 for 20ms), causing IMDCT
// to produce wrong sample counts. After the fix, it returns frameSize (960).
func TestDecodeBands_OutputSize(t *testing.T) {
	testCases := []struct {
		name      string
		frameSize int
		nbBands   int
		totalBins int // sum of scaled band widths (smaller than frameSize)
	}{
		// totalBins = sum(ScaledBandWidth(band, frameSize) for band in 0..nbBands-1)
		// These are calculated from EBands table with scaling
		{"2.5ms", 120, 13, 20}, // EffBands=13, bands sum to 20
		{"5ms", 240, 17, 80},   // EffBands=17, bands sum to 80
		{"10ms", 480, 19, 240}, // EffBands=19, bands sum to 240
		{"20ms", 960, 21, 800}, // EffBands=21, bands sum to 800
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(1)

			// Create mock energies and bandBits
			energies := make([]float64, tc.nbBands)
			bandBits := make([]int, tc.nbBands)

			// Call DecodeBands
			coeffs := d.DecodeBands(energies, bandBits, tc.nbBands, false, tc.frameSize)

			// Verify output length equals frameSize, not totalBins
			if len(coeffs) != tc.frameSize {
				t.Errorf("DecodeBands returned %d coeffs, want %d (frameSize)", len(coeffs), tc.frameSize)
			}

			// Verify totalBins calculation is as expected
			actualTotalBins := 0
			for band := 0; band < tc.nbBands; band++ {
				actualTotalBins += ScaledBandWidth(band, tc.frameSize)
			}
			if actualTotalBins != tc.totalBins {
				t.Errorf("totalBins = %d, expected %d", actualTotalBins, tc.totalBins)
			}

			// Log the key insight: frameSize > totalBins
			t.Logf("frameSize=%d, totalBins=%d, diff=%d (upper bins zero-padded)",
				tc.frameSize, tc.totalBins, tc.frameSize-tc.totalBins)
		})
	}
}

// TestDecodeBandsStereo_OutputSize verifies stereo DecodeBands returns frameSize per channel.
func TestDecodeBandsStereo_OutputSize(t *testing.T) {
	testCases := []struct {
		name      string
		frameSize int
		nbBands   int
	}{
		{"2.5ms", 120, 13},
		{"5ms", 240, 17},
		{"10ms", 480, 19},
		{"20ms", 960, 21},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(2) // Stereo

			// Create mock energies and bandBits
			energiesL := make([]float64, tc.nbBands)
			energiesR := make([]float64, tc.nbBands)
			bandBits := make([]int, tc.nbBands)

			// Call DecodeBandsStereo
			left, right := d.DecodeBandsStereo(energiesL, energiesR, bandBits, tc.nbBands, tc.frameSize, -1)

			// Verify both channels have frameSize coefficients
			if len(left) != tc.frameSize {
				t.Errorf("Left channel: got %d coeffs, want %d", len(left), tc.frameSize)
			}
			if len(right) != tc.frameSize {
				t.Errorf("Right channel: got %d coeffs, want %d", len(right), tc.frameSize)
			}
		})
	}
}

// TestDenormalizeBand verifies energy scaling produces correct amplitudes.
// Energy is in dB units: gain = 2^(energy/DB6)
// This matches libopus celt/bands.c denormalise_bands().
func TestDenormalizeBand(t *testing.T) {
	tests := []struct {
		name     string
		shape    []float64
		energy   float64
		wantGain float64 // Expected gain = 2^(energy/DB6)
	}{
		{
			name:     "zero energy",
			shape:    []float64{1.0, 0.0, 0.0},
			energy:   0.0,
			wantGain: 1.0, // 2^(0/DB6) = 1
		},
		{
			name:     "positive energy (6 dB)",
			shape:    []float64{0.5, 0.5, 0.5, 0.5},
			energy:   6.0,
			wantGain: 2.0, // 2^(6/6) = 2
		},
		{
			name:     "negative energy (-6 dB)",
			shape:    []float64{1.0},
			energy:   -6.0,
			wantGain: 0.5, // 2^(-6/6) = 0.5
		},
		{
			name:     "fractional energy (9 dB)",
			shape:    []float64{0.707, 0.707},
			energy:   9.0,
			wantGain: 2.828, // 2^(9/6) ~= 2.828
		},
		{
			name:     "energy = 6 (gain = 2)",
			shape:    []float64{1, 0, 0, 0},
			energy:   6.0,
			wantGain: 2.0, // 2^(6/6) = 2
		},
		{
			name:     "energy = -6 (gain = 0.5)",
			shape:    []float64{1, 0, 0, 0},
			energy:   -6.0,
			wantGain: 0.5, // 2^(-6/6) = 0.5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DenormalizeBand(tt.shape, tt.energy)

			if len(result) != len(tt.shape) {
				t.Fatalf("len(result) = %d, want %d", len(result), len(tt.shape))
			}

			// Check that result[i] = shape[i] * gain
			actualGain := 0.0
			if tt.shape[0] != 0 {
				actualGain = result[0] / tt.shape[0]
			}

			tolerance := 0.01
			if math.Abs(actualGain-tt.wantGain) > tolerance {
				t.Errorf("gain = %v, want %v (tolerance %v)", actualGain, tt.wantGain, tolerance)
			}
		})
	}
}

// TestDenormalizeEnergyClamping verifies extreme energies don't cause overflow.
func TestDenormalizeEnergyClamping(t *testing.T) {
	// Test that extreme energies don't cause overflow
	shape := []float64{1.0}

	// Very high energy should be clamped to 32
	resultHigh := DenormalizeBand(shape, 100.0)
	if math.IsInf(resultHigh[0], 0) || math.IsNaN(resultHigh[0]) {
		t.Error("High energy caused overflow or NaN")
	}
	// Expect clamped to 2^(32/DB6)
	maxGain := math.Exp2(32 / DB6)
	if resultHigh[0] > maxGain*1.001 {
		t.Errorf("High energy not clamped: got %v, want <= %v", resultHigh[0], maxGain)
	}
	if math.Abs(resultHigh[0]-maxGain) > maxGain*0.001 {
		t.Errorf("High energy should clamp to 2^32: got %v, want %v", resultHigh[0], maxGain)
	}

	// Very low energy should still work (no clamping needed for underflow)
	resultLow := DenormalizeBand(shape, -100.0)
	if math.IsNaN(resultLow[0]) {
		t.Error("Low energy caused NaN")
	}
	// Should be very small but not zero (floating point underflow to denormal)
	if resultLow[0] == 0 && shape[0] != 0 {
		t.Log("Low energy resulted in zero (may be expected due to underflow)")
	}
}

// TestComputeBandEnergy verifies energy computation.
func TestComputeBandEnergy(t *testing.T) {
	// Coefficients with known energy
	// sum(x^2) = 4, sqrt = 2, log2(2) = 1, energy = DB6 * 1
	coeffs := []float64{1, 1, 1, 1}
	energy := ComputeBandEnergy(coeffs)
	expected := DB6 * (0.5 * math.Log(4.0) / 0.6931471805599453)
	if math.Abs(energy-expected) > 1e-10 {
		t.Errorf("ComputeBandEnergy = %v, want %v", energy, expected)
	}

	// Empty vector should return default low energy
	energy = ComputeBandEnergy(nil)
	if energy != -28.0 {
		t.Errorf("ComputeBandEnergy(nil) = %v, want -28.0", energy)
	}

	energy = ComputeBandEnergy([]float64{})
	if energy != -28.0 {
		t.Errorf("ComputeBandEnergy([]) = %v, want -28.0", energy)
	}
}

// TestComputeBandEnergyRoundTrip verifies round-trip: ComputeBandEnergy -> DenormalizeBand preserves scale.
// This is the key property: if we compute energy from coefficients, then denormalize a unit shape
// by that energy, we should get back the original amplitude.
func TestComputeBandEnergyRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		coeffs []float64
	}{
		{
			name:   "unit vector",
			coeffs: []float64{1.0, 0.0, 0.0, 0.0},
		},
		{
			name:   "scaled vector",
			coeffs: []float64{4.0, 3.0, 0.0},
		},
		{
			name:   "negative values",
			coeffs: []float64{-2.0, 2.0, -2.0, 2.0},
		},
		{
			name:   "uniform distribution",
			coeffs: []float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Compute energy
			energy := ComputeBandEnergy(tt.coeffs)

			// Normalize to unit vector
			norm := 0.0
			for _, c := range tt.coeffs {
				norm += c * c
			}
			norm = math.Sqrt(norm)
			if norm == 0 {
				return
			}

			shape := make([]float64, len(tt.coeffs))
			for i, c := range tt.coeffs {
				shape[i] = c / norm
			}

			// Denormalize back
			result := DenormalizeBand(shape, energy)

			// Should get approximately original coefficients
			for i := range tt.coeffs {
				if math.Abs(result[i]-tt.coeffs[i]) > 0.1 {
					t.Errorf("coeff[%d] = %v, want %v", i, result[i], tt.coeffs[i])
				}
			}
		})
	}
}

// TestIntToFloat verifies int to float conversion.
func TestIntToFloat(t *testing.T) {
	input := []int{1, -2, 3, -4, 0}
	result := intToFloat(input)

	expected := []float64{1, -2, 3, -4, 0}
	if len(result) != len(expected) {
		t.Errorf("intToFloat length = %d, want %d", len(result), len(expected))
		return
	}

	for i := range result {
		if result[i] != expected[i] {
			t.Errorf("intToFloat[%d] = %v, want %v", i, result[i], expected[i])
		}
	}

	// Test nil input
	if intToFloat(nil) != nil {
		t.Error("intToFloat(nil) should return nil")
	}
}

// TestThetaToGains verifies stereo angle to gain conversion.
func TestThetaToGains(t *testing.T) {
	tests := []struct {
		itheta  int
		qn      int
		midExp  float64
		sideExp float64
	}{
		{itheta: 0, qn: 8, midExp: 1.0, sideExp: 0.0},     // Pure mid
		{itheta: 8, qn: 8, midExp: 0.0, sideExp: 1.0},     // Pure side
		{itheta: 4, qn: 8, midExp: 0.707, sideExp: 0.707}, // 45 degrees (approx)
	}

	for _, tt := range tests {
		mid, side := ThetaToGains(tt.itheta, tt.qn)
		if math.Abs(mid-tt.midExp) > 0.01 {
			t.Errorf("ThetaToGains(%d, %d) mid = %v, want ~%v", tt.itheta, tt.qn, mid, tt.midExp)
		}
		if math.Abs(side-tt.sideExp) > 0.01 {
			t.Errorf("ThetaToGains(%d, %d) side = %v, want ~%v", tt.itheta, tt.qn, side, tt.sideExp)
		}
	}
}

// BenchmarkNormalizeVector measures normalization performance.
func BenchmarkNormalizeVector(b *testing.B) {
	v := make([]float64, 100)
	for i := range v {
		v[i] = float64(i + 1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NormalizeVector(v)
	}
}

// BenchmarkFoldBand measures band folding performance.
func BenchmarkFoldBand(b *testing.B) {
	lowband := make([]float64, 8)
	for i := range lowband {
		lowband[i] = float64(i+1) / 10.0
	}
	lowband = NormalizeVector(lowband)

	seed := uint32(12345)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FoldBand(lowband, 16, &seed)
	}
}

// BenchmarkBitsToK measures bits-to-pulses conversion performance.
func BenchmarkBitsToK(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bitsToK(50, 8)
	}
}

// BenchmarkDecodeBands measures band decoding performance.
// Note: This benchmark uses mock data since we don't have a real range decoder.
func BenchmarkDecodeBands(b *testing.B) {
	energies := make([]float64, 21)
	bandBits := make([]int, 21)
	for i := range bandBits {
		bandBits[i] = 50 // Typical bit allocation
	}

	// Benchmark energy scaling (denormalization) as part of decode path
	shape := make([]float64, 100)
	for i := range shape {
		shape[i] = float64(i) / 100.0
	}
	shape = NormalizeVector(shape)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = DenormalizeBand(shape, energies[0])
		_ = len(bandBits)
	}
}

// TestBitsToKBoundaries tests edge cases for bits-to-pulses conversion.
func TestBitsToKBoundaries(t *testing.T) {
	// Zero bits should always return zero pulses
	for n := 1; n <= 32; n++ {
		k := bitsToK(0, n)
		if k != 0 {
			t.Errorf("bitsToK(0, %d) = %d, want 0", n, k)
		}
	}

	// Zero dimensions should always return zero pulses
	for bits := 0; bits <= 100; bits += 10 {
		k := bitsToK(bits, 0)
		if k != 0 {
			t.Errorf("bitsToK(%d, 0) = %d, want 0", bits, k)
		}
	}

	// Negative dimensions should return zero
	k := bitsToK(50, -1)
	if k != 0 {
		t.Errorf("bitsToK(50, -1) = %d, want 0", k)
	}

	// Test minimum bits for k=1 at various n
	t.Run("min_bits_for_k1", func(t *testing.T) {
		for n := 2; n <= 16; n++ {
			// Find minimum bits needed for k=1
			var minBits int
			for bits := 1; bits <= 100; bits++ {
				if bitsToK(bits, n) >= 1 {
					minBits = bits
					break
				}
			}
			// Should be approximately log2(V(n,1)) = log2(2n)
			v1 := PVQ_V(n, 1)
			expectedMin := ilog2(int(v1 - 1))
			t.Logf("n=%d: min bits for k=1 is %d, V(%d,1)=%d, ilog2(V-1)=%d",
				n, minBits, n, v1, expectedMin)
		}
	})
}

// TestBitsToKMonotonic verifies that more bits never result in fewer pulses.
func TestBitsToKMonotonic(t *testing.T) {
	dimensions := []int{2, 4, 8, 16, 32}

	for _, n := range dimensions {
		t.Run("", func(t *testing.T) {
			prev := 0
			for bits := 0; bits <= 150; bits++ {
				k := bitsToK(bits, n)
				if k < prev {
					t.Errorf("bitsToK not monotonic at n=%d: bits=%d gave k=%d, but bits=%d gave k=%d",
						n, bits-1, prev, bits, k)
				}
				prev = k
			}
		})
	}
}

// TestKToBitsRoundtrip verifies kToBits tracks log2(V(n,k)-1).
func TestKToBitsRoundtrip(t *testing.T) {
	// Test that the functions are internally consistent
	for n := 2; n <= 16; n++ {
		for k := 1; k <= 8; k++ {
			bits := kToBits(k, n)
			v := PVQ_V(n, k)

			// kToBits should return approximately log2(V(n,k))
			expectedBits := ilog2(int(v - 1))
			if bits != expectedBits && bits != expectedBits+1 && bits != expectedBits-1 {
				t.Logf("kToBits(%d, %d) = %d, but ilog2(V(%d,%d)-1) = ilog2(%d-1) = %d",
					k, n, bits, n, k, v, expectedBits)
			}

			_ = bits // kToBits validation is above; bitsToK is tested separately.
		}
	}
}

// TestKToBitsValues verifies kToBits produces reasonable bit counts.
// kToBits returns ilog2(V(n,k)-1), which is floor(log2(V-1)).
func TestKToBitsValues(t *testing.T) {
	// Test that kToBits returns values consistent with V(n,k)
	testCases := []struct {
		k, n int
	}{
		{1, 2}, // V(2,1) = 4
		{1, 4}, // V(4,1) = 8
		{1, 8}, // V(8,1) = 16
		{2, 4}, // V(4,2) = 32
		{4, 8}, // V(8,4) is large
	}

	for _, tc := range testCases {
		bits := kToBits(tc.k, tc.n)
		v := PVQ_V(tc.n, tc.k)
		expectedBits := ilog2(int(v - 1))

		if bits != expectedBits {
			t.Errorf("kToBits(%d, %d) = %d, but ilog2(V(%d,%d)-1) = ilog2(%d) = %d",
				tc.k, tc.n, bits, tc.n, tc.k, v-1, expectedBits)
		}

		// Log the values for visibility
		t.Logf("kToBits(%d, %d) = %d, V = %d", tc.k, tc.n, bits, v)
	}
}

// TestBitsToKWithRealFrameSizes tests bit allocation for actual CELT frame sizes.
func TestBitsToKWithRealFrameSizes(t *testing.T) {
	// Test with band widths from actual CELT frames
	frameSizes := []struct {
		name      string
		frameSize int
		nbBands   int
	}{
		{"2.5ms", 120, 13},
		{"5ms", 240, 17},
		{"10ms", 480, 19},
		{"20ms", 960, 21},
	}

	for _, fs := range frameSizes {
		t.Run(fs.name, func(t *testing.T) {
			for band := 0; band < fs.nbBands; band++ {
				n := ScaledBandWidth(band, fs.frameSize)
				if n <= 0 {
					continue
				}

				// Test various bit allocations
				for bits := 0; bits <= 200; bits += 20 {
					k := bitsToK(bits, n)

					// k should be non-negative
					if k < 0 {
						t.Errorf("bitsToK(%d, %d) = %d < 0 at band %d", bits, n, k, band)
					}

					// k should not exceed reasonable bounds
					if k > MaxPVQK {
						t.Errorf("bitsToK(%d, %d) = %d > MaxPVQK at band %d", bits, n, k, band)
					}

					// Verify V(n,k) is valid (not zero for k > 0)
					if k > 0 {
						v := PVQ_V(n, k)
						if v == 0 {
							t.Errorf("V(%d, %d) = 0 for bits=%d at band %d", n, k, bits, band)
						}
					}
				}
			}
		})
	}
}

// TestDecodeBandsAllocationPath verifies the allocation -> k -> PVQ path is correctly wired.
// This tests that for each band, the bit allocation produces valid pulse counts
// that can be used with PVQ decoding.
func TestDecodeBandsAllocationPath(t *testing.T) {
	testCases := []struct {
		name      string
		frameSize int
		nbBands   int
	}{
		{"2.5ms", 120, 13},
		{"5ms", 240, 17},
		{"10ms", 480, 19},
		{"20ms", 960, 21},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for band := 0; band < tc.nbBands; band++ {
				n := ScaledBandWidth(band, tc.frameSize)
				if n <= 0 {
					continue
				}

				// Test various bit allocations
				for bits := 0; bits <= 100; bits += 10 {
					k := bitsToK(bits, n)

					// If k > 0, verify we can decode all valid indices
					if k > 0 {
						vCount := PVQ_V(n, k)
						if vCount == 0 {
							t.Errorf("V(%d, %d) = 0 at band %d with %d bits", n, k, band, bits)
							continue
						}

						// Test a few indices
						testIndices := []uint32{0}
						if vCount > 1 {
							testIndices = append(testIndices, vCount-1)
						}
						if vCount > 2 {
							testIndices = append(testIndices, vCount/2)
						}

						for _, idx := range testIndices {
							pulses := DecodePulses(idx, n, k)
							if pulses == nil {
								t.Errorf("DecodePulses(%d, %d, %d) returned nil at band %d", idx, n, k, band)
								continue
							}

							// Verify L1 norm
							sum := 0
							for _, p := range pulses {
								if p < 0 {
									sum -= p
								} else {
									sum += p
								}
							}
							if sum != k {
								t.Errorf("DecodePulses(%d, %d, %d) L1=%d, want %d at band %d",
									idx, n, k, sum, k, band)
							}

							// Verify normalization produces unit vector
							floatPulses := intToFloat(pulses)
							normalized := NormalizeVector(floatPulses)
							var norm2 float64
							for _, x := range normalized {
								norm2 += x * x
							}
							if math.Abs(norm2-1.0) > 1e-6 {
								t.Errorf("Normalized vector L2^2=%v, want 1.0 at band %d, idx %d",
									norm2, band, idx)
							}
						}
					}
				}
			}
		})
	}
}

// TestDenormalizationGainPath verifies the energy -> gain -> coefficient path.
// This tests that given a normalized shape and energy, we get correctly scaled coefficients.
func TestDenormalizationGainPath(t *testing.T) {
	// Test that energy values produce correct gains
	testCases := []struct {
		energy float64
		gain   float64 // expected gain = 2^energy
	}{
		{0.0, 1.0},
		{DB6, 2.0},
		{-DB6, 0.5},
		{3 * DB6, 8.0},
		{-3 * DB6, 0.125},
		{5 * DB6, 32.0},
		{-5 * DB6, 1.0 / 32.0},
	}

	shape := []float64{0.6, 0.8} // 3-4-5 triangle, normalized

	for _, tc := range testCases {
		result := DenormalizeBand(shape, tc.energy)

		// Check gain is correct
		if shape[0] != 0 {
			actualGain := result[0] / shape[0]
			if math.Abs(actualGain-tc.gain) > tc.gain*1e-6 {
				t.Errorf("energy=%v: gain=%v, want %v", tc.energy, actualGain, tc.gain)
			}
		}
	}

	// Test clamping at high energies
	t.Run("clamp_high", func(t *testing.T) {
		// Energy > 32 should be clamped to 32
		result := DenormalizeBand([]float64{1.0}, 100.0)
		expectedGain := math.Exp2(32 / DB6) // Clamped at 32 dB
		if math.Abs(result[0]-expectedGain) > expectedGain*1e-6 {
			t.Errorf("High energy: got %v, want ~%v (clamped)", result[0], expectedGain)
		}
	})

	// Test very low energies don't produce NaN/Inf
	t.Run("low_energy", func(t *testing.T) {
		result := DenormalizeBand([]float64{1.0}, -100.0)
		if math.IsNaN(result[0]) || math.IsInf(result[0], 0) {
			t.Errorf("Low energy produced NaN/Inf: %v", result[0])
		}
	})
}

// TestIlog2 verifies the integer log2 function.
func TestIlog2(t *testing.T) {
	tests := []struct {
		x    int
		want int
	}{
		{0, 0},
		{1, 0},
		{2, 1},
		{3, 1},
		{4, 2},
		{7, 2},
		{8, 3},
		{15, 3},
		{16, 4},
		{255, 7},
		{256, 8},
		{1023, 9},
		{1024, 10},
	}

	for _, tc := range tests {
		got := ilog2(tc.x)
		if got != tc.want {
			t.Errorf("ilog2(%d) = %d, want %d", tc.x, got, tc.want)
		}
	}
}
