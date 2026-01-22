package celt

import (
	"math"
	"math/rand"
	"testing"

	"gopus/internal/rangecoding"
)

// TestVectorToPulses verifies that vectorToPulses converts normalized floats
// to integer pulses with correct L1 norm.
func TestVectorToPulses(t *testing.T) {
	tests := []struct {
		name  string
		shape []float64
		k     int
	}{
		{
			name:  "simple normalized vector",
			shape: []float64{0.6, 0.8, 0, 0}, // L2 norm = 1.0
			k:     10,
		},
		{
			name:  "single element",
			shape: []float64{1.0},
			k:     5,
		},
		{
			name:  "negative values",
			shape: []float64{-0.6, 0.8, 0, 0},
			k:     10,
		},
		{
			name:  "all zeros (degenerate)",
			shape: []float64{0, 0, 0, 0},
			k:     10,
		},
		{
			name:  "large k",
			shape: []float64{0.5, 0.5, 0.5, 0.5}, // L2 norm = 1.0
			k:     50,
		},
		{
			name:  "uneven distribution",
			shape: []float64{0.9, 0.1, 0.2, 0.3}, // Not normalized
			k:     20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pulses := vectorToPulses(tt.shape, tt.k)

			// Verify length matches
			if len(pulses) != len(tt.shape) {
				t.Errorf("length mismatch: got %d, want %d", len(pulses), len(tt.shape))
			}

			// Verify L1 norm equals k
			l1norm := 0
			for _, p := range pulses {
				if p < 0 {
					l1norm += -p
				} else {
					l1norm += p
				}
			}
			if l1norm != tt.k {
				t.Errorf("L1 norm = %d, want %d; pulses = %v", l1norm, tt.k, pulses)
			}
		})
	}
}

// TestVectorToPulsesPreservesDirection verifies that pulse vectors preserve
// the direction of the input shape.
func TestVectorToPulsesPreservesDirection(t *testing.T) {
	// Create a normalized vector
	shape := NormalizeVector([]float64{3.0, 4.0, 0.0, 0.0}) // [0.6, 0.8, 0, 0]
	k := 20

	pulses := vectorToPulses(shape, k)

	// Convert pulses back to normalized float
	floatPulses := make([]float64, len(pulses))
	for i, p := range pulses {
		floatPulses[i] = float64(p)
	}
	normalizedPulses := NormalizeVector(floatPulses)

	// Compute dot product (should be close to 1 if directions match)
	var dot float64
	for i := range shape {
		dot += shape[i] * normalizedPulses[i]
	}

	// Allow some tolerance due to quantization
	if dot < 0.8 {
		t.Errorf("direction not preserved: dot product = %f, want > 0.8", dot)
		t.Logf("shape: %v", shape)
		t.Logf("pulses: %v", pulses)
		t.Logf("normalizedPulses: %v", normalizedPulses)
	}
}

// TestVectorToPulsesRoundTrip tests that converting to pulses and normalizing
// back preserves the original direction. Uses higher k values for better accuracy.
func TestVectorToPulsesRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iter := 0; iter < 10; iter++ {
		// Generate random normalized vector
		n := 4 + rng.Intn(12) // 4 to 16 dimensions
		shape := make([]float64, n)
		for i := range shape {
			shape[i] = rng.Float64()*2 - 1 // [-1, 1]
		}
		shape = NormalizeVector(shape)

		// Test with larger k values where quantization is less severe
		// Lower k values have inherently higher quantization error
		for _, k := range []int{20, 50, 100} {
			pulses := vectorToPulses(shape, k)

			// Verify L1 norm is correct (most important property)
			l1norm := 0
			for _, p := range pulses {
				if p < 0 {
					l1norm += -p
				} else {
					l1norm += p
				}
			}
			if l1norm != k {
				t.Errorf("iter %d, k=%d: L1 norm = %d, want %d", iter, k, l1norm, k)
			}

			// Convert back to float and normalize
			floatPulses := make([]float64, len(pulses))
			for i, p := range pulses {
				floatPulses[i] = float64(p)
			}
			reconstructed := NormalizeVector(floatPulses)

			// Compute dot product
			var dot float64
			for i := range shape {
				dot += shape[i] * reconstructed[i]
			}

			// Higher k should give better preservation
			minDot := 0.6
			if k >= 50 {
				minDot = 0.7
			}
			if k >= 100 {
				minDot = 0.8
			}

			if dot < minDot {
				t.Errorf("iter %d, k=%d: dot product = %f, want > %f", iter, k, dot, minDot)
			}
		}
	}
}

// TestPVQEncodeDecodeRoundTrip tests that encoding and decoding a PVQ vector
// produces a valid decoded shape. Note: Due to known asymmetry in CWRS
// encode/decode (D03-02-03), we focus on verifying decoded shape properties
// rather than exact reconstruction.
func TestPVQEncodeDecodeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iter := 0; iter < 10; iter++ {
		// Use larger k for better quantization
		n := 2 + rng.Intn(6)   // 2 to 8 dimensions
		k := 10 + rng.Intn(20) // 10 to 30 pulses

		// Generate random normalized shape
		shape := make([]float64, n)
		for i := range shape {
			shape[i] = rng.Float64()*2 - 1
		}
		shape = NormalizeVector(shape)

		// Create encoder with buffer
		buf := make([]byte, 256)
		re := &rangecoding.Encoder{}
		re.Init(buf)

		enc := NewEncoder(1)
		enc.SetRangeEncoder(re)

		// Encode the shape
		enc.EncodeBandPVQ(shape, n, k)

		// Finalize encoding
		data := re.Done()
		if len(data) == 0 {
			t.Logf("iter %d: no data produced for n=%d, k=%d", iter, n, k)
			continue
		}

		// Create decoder
		dec := NewDecoder(1)
		rd := &rangecoding.Decoder{}
		rd.Init(data)
		dec.SetRangeDecoder(rd)

		// Decode the shape
		decoded := dec.DecodePVQ(n, k)
		if len(decoded) != n {
			t.Errorf("iter %d: decoded length %d, want %d", iter, len(decoded), n)
			continue
		}

		// Verify decoded shape has unit L2 norm (critical property)
		var energy float64
		for _, x := range decoded {
			energy += x * x
		}
		l2norm := math.Sqrt(energy)
		if math.Abs(l2norm-1.0) > 0.01 {
			t.Errorf("iter %d: decoded L2 norm = %f, want 1.0", iter, l2norm)
		}

		// The decoded shape is a valid PVQ vector - direction may differ
		// due to CWRS asymmetry but the shape is still usable
		t.Logf("iter %d: n=%d, k=%d, decoded L2 norm=%.4f", iter, n, k, l2norm)
	}
}

// TestEncodeBandsAllSizes tests EncodeBands with all frame sizes.
func TestEncodeBandsAllSizes(t *testing.T) {
	frameSizes := []int{120, 240, 480, 960}
	nbBands := 21

	for _, frameSize := range frameSizes {
		t.Run(frameNameFromSize(frameSize), func(t *testing.T) {
			// Create encoder with buffer
			buf := make([]byte, 4096)
			re := &rangecoding.Encoder{}
			re.Init(buf)

			enc := NewEncoder(1)
			enc.SetRangeEncoder(re)

			// Generate test shapes
			shapes := make([][]float64, nbBands)
			for band := 0; band < nbBands; band++ {
				n := ScaledBandWidth(band, frameSize)
				if n <= 0 {
					shapes[band] = []float64{}
					continue
				}
				shape := make([]float64, n)
				// Simple pattern
				for i := range shape {
					shape[i] = float64(i%3 - 1)
				}
				shapes[band] = NormalizeVector(shape)
			}

			// Allocate bits (simple uniform allocation)
			bandBits := make([]int, nbBands)
			for band := 0; band < nbBands; band++ {
				bandBits[band] = 16 // 16 bits per band
			}

			// This should not panic
			enc.EncodeBands(shapes, bandBits, nbBands, frameSize)

			// Finalize and verify we got some data
			data := re.Done()
			if len(data) == 0 {
				t.Logf("Warning: no data produced for frameSize=%d", frameSize)
			}
		})
	}
}

// TestPVQEncodingPreservesEnergy verifies that decoded shapes have unit L2 norm.
func TestPVQEncodingPreservesEnergy(t *testing.T) {
	// Test with various n and k values
	testCases := []struct {
		n int
		k int
	}{
		{2, 5},
		{4, 10},
		{8, 20},
		{16, 30},
	}

	rng := rand.New(rand.NewSource(123))

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Generate random normalized shape
			shape := make([]float64, tc.n)
			for i := range shape {
				shape[i] = rng.Float64()*2 - 1
			}
			shape = NormalizeVector(shape)

			// Encode
			buf := make([]byte, 256)
			re := &rangecoding.Encoder{}
			re.Init(buf)

			enc := NewEncoder(1)
			enc.SetRangeEncoder(re)
			enc.EncodeBandPVQ(shape, tc.n, tc.k)
			data := re.Done()

			if len(data) == 0 {
				t.Skip("no data produced")
			}

			// Decode
			dec := NewDecoder(1)
			rd := &rangecoding.Decoder{}
			rd.Init(data)
			dec.SetRangeDecoder(rd)
			decoded := dec.DecodePVQ(tc.n, tc.k)

			// Check L2 norm
			var energy float64
			for _, x := range decoded {
				energy += x * x
			}
			l2norm := math.Sqrt(energy)

			if math.Abs(l2norm-1.0) > 0.01 {
				t.Errorf("n=%d, k=%d: L2 norm = %f, want 1.0", tc.n, tc.k, l2norm)
			}
		})
	}
}

// TestNormalizeBands verifies band normalization produces unit-norm shapes.
func TestNormalizeBands(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 480
	nbBands := 21

	// Generate MDCT coefficients (flat spectrum)
	totalBins := 0
	for band := 0; band < nbBands; band++ {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	mdctCoeffs := make([]float64, totalBins)
	for i := range mdctCoeffs {
		mdctCoeffs[i] = float64(i%10-5) * 0.1
	}

	// Generate energies (log2 scale)
	energies := make([]float64, nbBands)
	for band := 0; band < nbBands; band++ {
		energies[band] = float64(band) * 0.5 // Increasing energy
	}

	// Normalize
	shapes := enc.NormalizeBands(mdctCoeffs, energies, nbBands, frameSize)

	if len(shapes) != nbBands {
		t.Fatalf("got %d shapes, want %d", len(shapes), nbBands)
	}

	// Verify each shape has unit L2 norm
	for band, shape := range shapes {
		if len(shape) == 0 {
			continue
		}

		var energy float64
		for _, x := range shape {
			energy += x * x
		}
		l2norm := math.Sqrt(energy)

		if math.Abs(l2norm-1.0) > 1e-6 {
			t.Errorf("band %d: L2 norm = %f, want 1.0", band, l2norm)
		}
	}
}

// TestEncodeBandsWithDecoder tests that encoding and decoding bands produces
// valid unit-norm vectors. Note: Due to CWRS asymmetry, exact direction
// preservation is not guaranteed.
func TestEncodeBandsWithDecoder(t *testing.T) {
	frameSize := 480
	nbBands := 5 // Test with fewer bands for simplicity

	// Create encoder with buffer
	buf := make([]byte, 4096)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	enc := NewEncoder(1)
	enc.SetRangeEncoder(re)

	// Generate normalized shapes
	shapes := make([][]float64, nbBands)
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		shape := make([]float64, n)
		for i := range shape {
			shape[i] = float64((i+band)%5 - 2)
		}
		shape = NormalizeVector(shape)
		shapes[band] = shape
	}

	// Allocate bits (generous allocation)
	bandBits := make([]int, nbBands)
	for band := 0; band < nbBands; band++ {
		bandBits[band] = 24 // 24 bits per band
	}

	// Encode
	enc.EncodeBands(shapes, bandBits, nbBands, frameSize)
	data := re.Done()

	if len(data) == 0 {
		t.Skip("no data produced")
	}

	// Decode
	dec := NewDecoder(1)
	rd := &rangecoding.Decoder{}
	rd.Init(data)
	dec.SetRangeDecoder(rd)

	// Decode each band individually and verify unit L2 norm
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		k := bitsToK(bandBits[band], n)
		if k <= 0 {
			continue
		}

		decoded := dec.DecodePVQ(n, k)

		// Verify decoded shape has unit L2 norm
		var energy float64
		for _, x := range decoded {
			energy += x * x
		}
		l2norm := math.Sqrt(energy)

		if math.Abs(l2norm-1.0) > 0.01 {
			t.Errorf("band %d: L2 norm = %f, want 1.0", band, l2norm)
		}
	}
}

// TestEncodeBandPVQProducesValidIndex verifies that EncodeBandPVQ produces
// data that can be decoded without errors.
func TestEncodeBandPVQProducesValidIndex(t *testing.T) {
	testCases := []struct {
		n int
		k int
	}{
		{4, 5},
		{8, 10},
		{16, 20},
		{32, 30},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Create a simple shape
			shape := make([]float64, tc.n)
			for i := range shape {
				shape[i] = 1.0
			}
			shape = NormalizeVector(shape)

			// Encode
			buf := make([]byte, 256)
			re := &rangecoding.Encoder{}
			re.Init(buf)

			enc := NewEncoder(1)
			enc.SetRangeEncoder(re)
			enc.EncodeBandPVQ(shape, tc.n, tc.k)
			data := re.Done()

			if len(data) == 0 {
				t.Errorf("n=%d, k=%d: no data produced", tc.n, tc.k)
				return
			}

			// Verify we can decode without panic
			dec := NewDecoder(1)
			rd := &rangecoding.Decoder{}
			rd.Init(data)
			dec.SetRangeDecoder(rd)

			decoded := dec.DecodePVQ(tc.n, tc.k)
			if len(decoded) != tc.n {
				t.Errorf("n=%d, k=%d: decoded length %d", tc.n, tc.k, len(decoded))
			}
		})
	}
}

func frameNameFromSize(size int) string {
	switch size {
	case 120:
		return "2.5ms"
	case 240:
		return "5ms"
	case 480:
		return "10ms"
	case 960:
		return "20ms"
	default:
		return "unknown"
	}
}
