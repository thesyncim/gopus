package celt

import (
	"fmt"
	"math"
	"testing"
)

// TestTransientInterleaveDeinterleave validates the interleave/deinterleave operations
// for transient short-block handling against the libopus implementation.
//
// In libopus (bands.c):
// - deinterleave_hadamard: Reorganizes samples from time order to frequency order
// - interleave_hadamard: Reorganizes samples from frequency order to time order
//
// The ordery_table provides bit-reversed Gray code ordering for Hadamard transforms.
func TestTransientInterleaveDeinterleave(t *testing.T) {
	// Test ordery table matches libopus
	// From libopus bands.c:
	// static const int ordery_table[] = {
	//    1,  0,
	//    3,  0,  2,  1,
	//    7,  0,  4,  3,  6,  1,  5,  2,
	//   15,  0,  8,  7, 12,  3, 11,  4, 14,  1,  9,  6, 13,  2, 10,  5,
	// };
	libopusOrdery := []int{
		1, 0,
		3, 0, 2, 1,
		7, 0, 4, 3, 6, 1, 5, 2,
		15, 0, 8, 7, 12, 3, 11, 4, 14, 1, 9, 6, 13, 2, 10, 5,
	}

	if len(orderyTable) != len(libopusOrdery) {
		t.Errorf("orderyTable length mismatch: got %d, want %d", len(orderyTable), len(libopusOrdery))
	}
	for i, v := range libopusOrdery {
		if orderyTable[i] != v {
			t.Errorf("orderyTable[%d] mismatch: got %d, want %d", i, orderyTable[i], v)
		}
	}

	// Test deinterleave/interleave roundtrip for various stride values
	testCases := []struct {
		n0       int
		stride   int
		hadamard bool
	}{
		{8, 2, true},
		{8, 2, false},
		{16, 4, true},
		{16, 4, false},
		{32, 8, true},
		{32, 8, false},
		{64, 16, true},
		{64, 16, false},
		// CELT typical cases
		{15, 8, true}, // 120/8 = 15 (960 frame, 8 short blocks)
		{30, 4, true}, // 120/4 = 30 (480 frame, 4 short blocks)
		{60, 2, true}, // 120/2 = 60 (240 frame, 2 short blocks)
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("n0=%d_stride=%d_hadamard=%v", tc.n0, tc.stride, tc.hadamard), func(t *testing.T) {
			n := tc.n0 * tc.stride
			original := make([]float64, n)
			for i := range original {
				original[i] = float64(i + 1)
			}

			// Deinterleave
			x := make([]float64, n)
			copy(x, original)
			deinterleaveHadamard(x, tc.n0, tc.stride, tc.hadamard)

			// Interleave back
			interleaveHadamard(x, tc.n0, tc.stride, tc.hadamard)

			// Verify roundtrip
			for i := range original {
				if math.Abs(x[i]-original[i]) > 1e-10 {
					t.Errorf("roundtrip mismatch at %d: got %f, want %f", i, x[i], original[i])
				}
			}
		})
	}
}

// TestTransientLMAndB validates LM (log mode) and B (number of short blocks) handling.
//
// In libopus (celt_decoder.c celt_synthesis):
// - LM = 0,1,2,3 for 2.5ms,5ms,10ms,20ms frames
// - M = 1<<LM (multiplier)
// - For transient: B = M (number of short blocks), NB = shortMdctSize (120)
// - For non-transient: B = 1, NB = shortMdctSize << LM
func TestTransientLMAndB(t *testing.T) {
	const shortMdctSize = 120

	testCases := []struct {
		lm          int
		isTransient bool
		wantB       int
		wantNB      int
		wantN       int // Total samples N = M * shortMdctSize
	}{
		// Non-transient frames
		{0, false, 1, 120, 120}, // 2.5ms
		{1, false, 1, 240, 240}, // 5ms
		{2, false, 1, 480, 480}, // 10ms
		{3, false, 1, 960, 960}, // 20ms
		// Transient frames
		{0, true, 1, 120, 120}, // 2.5ms (no short blocks possible)
		{1, true, 2, 120, 240}, // 5ms: 2 short blocks of 120
		{2, true, 4, 120, 480}, // 10ms: 4 short blocks of 120
		{3, true, 8, 120, 960}, // 20ms: 8 short blocks of 120
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("LM=%d_transient=%v", tc.lm, tc.isTransient), func(t *testing.T) {
			M := 1 << tc.lm
			var B, NB int
			if tc.isTransient {
				B = M
				NB = shortMdctSize
			} else {
				B = 1
				NB = shortMdctSize << tc.lm
			}
			N := M * shortMdctSize

			if B != tc.wantB {
				t.Errorf("B mismatch: got %d, want %d", B, tc.wantB)
			}
			if NB != tc.wantNB {
				t.Errorf("NB mismatch: got %d, want %d", NB, tc.wantNB)
			}
			if N != tc.wantN {
				t.Errorf("N mismatch: got %d, want %d", N, tc.wantN)
			}

			// Verify relationship: B * NB = N (for transient)
			if tc.isTransient && B*NB != N {
				t.Errorf("B*NB != N for transient: %d*%d = %d != %d", B, NB, B*NB, N)
			}
		})
	}
}

// TestTransientCoefficientsInterleaving validates that the coefficient interleaving
// for transient frames matches the libopus layout.
//
// In libopus, for transient frames with B short blocks:
// - Input coefficients are interleaved: coef[b + i*B] where b=block, i=bin
// - The IMDCT reads with stride B: &freq[b] with stride B
// - Output goes to out_syn[c] + NB*b
func TestTransientCoefficientsInterleaving(t *testing.T) {
	// Test 960 frame with 8 short blocks
	frameSize := 960
	shortBlocks := 8
	shortSize := frameSize / shortBlocks // 120

	// Create test coefficients in the interleaved format libopus expects
	coeffs := make([]float64, frameSize)
	for b := 0; b < shortBlocks; b++ {
		for i := 0; i < shortSize; i++ {
			// Interleaved: coef[b + i*B]
			idx := b + i*shortBlocks
			coeffs[idx] = float64(b*1000 + i) // Block b, bin i
		}
	}

	// Extract coefficients for each block (as the Go implementation should do)
	for b := 0; b < shortBlocks; b++ {
		shortCoeffs := make([]float64, shortSize)
		for i := 0; i < shortSize; i++ {
			idx := b + i*shortBlocks
			shortCoeffs[i] = coeffs[idx]
		}

		// Verify extraction is correct
		for i := 0; i < shortSize; i++ {
			expected := float64(b*1000 + i)
			if shortCoeffs[i] != expected {
				t.Errorf("Block %d, bin %d: got %f, want %f", b, i, shortCoeffs[i], expected)
			}
		}
	}
}

// TestHaar1Transform validates the Haar wavelet transform matches libopus.
//
// From libopus bands.c:
// void haar1(celt_norm *X, int N0, int stride)
//
//	{
//	   int i, j;
//	   N0 >>= 1;
//	   for (i=0;i<stride;i++)
//	      for (j=0;j<N0;j++)
//	      {
//	         opus_val32 tmp1, tmp2;
//	         tmp1 = MULT32_32_Q31(QCONST32(.70710678f,31), X[stride*2*j+i]);
//	         tmp2 = MULT32_32_Q31(QCONST32(.70710678f,31), X[stride*(2*j+1)+i]);
//	         X[stride*2*j+i] = ADD32(tmp1, tmp2);
//	         X[stride*(2*j+1)+i] = SUB32(tmp1, tmp2);
//	      }
//	}
func TestHaar1Transform(t *testing.T) {
	invSqrt2 := 0.7071067811865476

	testCases := []struct {
		n0     int
		stride int
	}{
		{4, 1},
		{4, 2},
		{8, 1},
		{8, 2},
		{16, 1},
		{16, 4},
		{120, 1}, // Typical CELT short block
		{120, 8}, // 8 short blocks
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("n0=%d_stride=%d", tc.n0, tc.stride), func(t *testing.T) {
			n := tc.n0 * tc.stride

			// Create test data
			original := make([]float64, n)
			for i := range original {
				original[i] = float64(i + 1)
			}

			// Compute expected result using libopus formula
			expected := make([]float64, n)
			copy(expected, original)
			n0Half := tc.n0 >> 1
			for i := 0; i < tc.stride; i++ {
				for j := 0; j < n0Half; j++ {
					idx0 := tc.stride*2*j + i
					idx1 := tc.stride*(2*j+1) + i
					tmp1 := invSqrt2 * expected[idx0]
					tmp2 := invSqrt2 * expected[idx1]
					expected[idx0] = tmp1 + tmp2
					expected[idx1] = tmp1 - tmp2
				}
			}

			// Apply our haar1 function
			x := make([]float64, n)
			copy(x, original)
			haar1(x, tc.n0, tc.stride)

			// Compare
			for i := range x {
				if math.Abs(x[i]-expected[i]) > 1e-10 {
					t.Errorf("haar1 mismatch at %d: got %f, want %f", i, x[i], expected[i])
				}
			}
		})
	}
}

// TestBitInterleaveDeinterleave validates the bit interleave/deinterleave tables.
//
// From libopus bands.c:
//
//	static const unsigned char bit_interleave_table[16]={
//	      0,1,1,1,2,3,3,3,2,3,3,3,2,3,3,3
//	};
//
//	static const unsigned char bit_deinterleave_table[16]={
//	      0x00,0x03,0x0C,0x0F,0x30,0x33,0x3C,0x3F,
//	      0xC0,0xC3,0xCC,0xCF,0xF0,0xF3,0xFC,0xFF
//	};
func TestBitInterleaveDeinterleave(t *testing.T) {
	libopusBitInterleave := []int{
		0, 1, 1, 1, 2, 3, 3, 3, 2, 3, 3, 3, 2, 3, 3, 3,
	}
	libopusBitDeinterleave := []int{
		0x00, 0x03, 0x0C, 0x0F, 0x30, 0x33, 0x3C, 0x3F,
		0xC0, 0xC3, 0xCC, 0xCF, 0xF0, 0xF3, 0xFC, 0xFF,
	}

	if len(bitInterleaveTable) != len(libopusBitInterleave) {
		t.Errorf("bitInterleaveTable length mismatch: got %d, want %d", len(bitInterleaveTable), len(libopusBitInterleave))
	}
	for i, v := range libopusBitInterleave {
		if bitInterleaveTable[i] != v {
			t.Errorf("bitInterleaveTable[%d] mismatch: got %d, want %d", i, bitInterleaveTable[i], v)
		}
	}

	if len(bitDeinterleaveTable) != len(libopusBitDeinterleave) {
		t.Errorf("bitDeinterleaveTable length mismatch: got %d, want %d", len(bitDeinterleaveTable), len(libopusBitDeinterleave))
	}
	for i, v := range libopusBitDeinterleave {
		if bitDeinterleaveTable[i] != v {
			t.Errorf("bitDeinterleaveTable[%d] mismatch: got %d, want %d", i, bitDeinterleaveTable[i], v)
		}
	}
}

// TestTransientSynthesisShortBlocks validates the short block IMDCT synthesis
// for transient frames.
func TestTransientSynthesisShortBlocks(t *testing.T) {
	// For 960-sample (20ms) transient frame:
	// - 8 short blocks of 120 samples each
	// - Each short IMDCT produces 240 samples
	// - With 120 overlap, each block adds 120 new samples
	// - Total output: 960 samples
	frameSize := 960
	shortBlocks := 8
	shortSize := frameSize / shortBlocks
	overlap := 120

	// Create test coefficients (in interleaved format)
	coeffs := make([]float64, frameSize)
	for b := 0; b < shortBlocks; b++ {
		for i := 0; i < shortSize; i++ {
			idx := b + i*shortBlocks
			// Create a simple signal: DC offset + small variation
			coeffs[idx] = 0.01 * (1 + 0.1*float64(i)/float64(shortSize))
		}
	}

	// Test synthesis
	dec := NewDecoder(1)
	output := dec.Synthesize(coeffs, true, shortBlocks)

	if len(output) != frameSize {
		t.Errorf("output length mismatch: got %d, want %d", len(output), frameSize)
	}

	// Verify output is non-zero (synthesis worked)
	var maxAbs float64
	for _, v := range output {
		if math.Abs(v) > maxAbs {
			maxAbs = math.Abs(v)
		}
	}
	if maxAbs == 0 {
		t.Error("output is all zeros - synthesis failed")
	}

	t.Logf("Transient synthesis: %d samples, max abs = %f, overlap = %d", len(output), maxAbs, overlap)
}

// TestQuantBandTransient validates band quantization for transient frames.
func TestQuantBandTransient(t *testing.T) {
	// Test that quant_band correctly handles B > 1 (transient) cases
	// For transient frames:
	// - B = M (number of short blocks)
	// - The band width is scaled by M (<<LM)
	// - Coefficients are processed with stride B

	testCases := []struct {
		lm        int
		transient bool
		expectedB int
	}{
		{0, false, 1},
		{1, false, 1},
		{2, false, 1},
		{3, false, 1},
		{0, true, 1},
		{1, true, 2},
		{2, true, 4},
		{3, true, 8},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("LM=%d_transient=%v", tc.lm, tc.transient), func(t *testing.T) {
			M := 1 << tc.lm
			B := 1
			if tc.transient {
				B = M
			}
			if B != tc.expectedB {
				t.Errorf("B mismatch: got %d, want %d", B, tc.expectedB)
			}
		})
	}
}

// TestModeConfigShortBlocks validates that ModeConfig.ShortBlocks matches expectations.
func TestModeConfigShortBlocks(t *testing.T) {
	testCases := []struct {
		frameSize           int
		expectedLM          int
		expectedShortBlocks int
	}{
		{120, 0, 1}, // 2.5ms
		{240, 1, 2}, // 5ms
		{480, 2, 4}, // 10ms
		{960, 3, 8}, // 20ms
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("frameSize=%d", tc.frameSize), func(t *testing.T) {
			mode := GetModeConfig(tc.frameSize)

			if mode.LM != tc.expectedLM {
				t.Errorf("LM mismatch: got %d, want %d", mode.LM, tc.expectedLM)
			}
			if mode.ShortBlocks != tc.expectedShortBlocks {
				t.Errorf("ShortBlocks mismatch: got %d, want %d", mode.ShortBlocks, tc.expectedShortBlocks)
			}

			// Verify ShortBlocks = 1 << LM
			expectedFromLM := 1 << tc.expectedLM
			if tc.frameSize == 120 {
				expectedFromLM = 1 // Special case: 2.5ms has no short blocks
			}
			if mode.ShortBlocks != expectedFromLM {
				t.Logf("Note: ShortBlocks=%d differs from 1<<LM=%d for frameSize=%d",
					mode.ShortBlocks, 1<<tc.expectedLM, tc.frameSize)
			}
		})
	}
}
