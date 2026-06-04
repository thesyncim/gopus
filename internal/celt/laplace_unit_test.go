// Package celt implements unit tests for Laplace encoding/decoding.
// This is a port of libopus celt/tests/test_unit_laplace.c
package celt

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Constants from libopus laplace.c
const (
	// LAPLACE_LOG_MINP = 0
	// LAPLACE_MINP = 1 << LAPLACE_LOG_MINP = 1
	testLaplaceMinP = 1
	// LAPLACE_NMIN = 16
	testLaplaceNMin = 16
)

// ecLaplaceGetStartFreq computes the start frequency for Laplace coding.
// This matches the ec_laplace_get_start_freq function in test_unit_laplace.c:
//
//	int ec_laplace_get_start_freq(int decay)
//	{
//	   opus_uint32 ft = 32768 - LAPLACE_MINP*(2*LAPLACE_NMIN+1);
//	   int fs = (ft*(16384-decay))/(16384+decay);
//	   return fs+LAPLACE_MINP;
//	}
func ecLaplaceGetStartFreq(decay int) int {
	ft := uint32(32768 - testLaplaceMinP*(2*testLaplaceNMin+1))
	fs := int((ft * uint32(16384-decay)) / uint32(16384+decay))
	return fs + testLaplaceMinP
}

// TestLaplaceRoundTripUnit is a direct port of test_unit_laplace.c main().
// It encodes 10000 values with Laplace coding and verifies decoding matches.
func TestLaplaceRoundTripUnit(t *testing.T) {
	const dataSize = 40000
	const numValues = 10000

	// Allocate arrays for values and decay parameters
	val := make([]int, numValues)
	decay := make([]int, numValues)

	// Initialize test data exactly as in the C test
	val[0] = 3
	decay[0] = 6000
	val[1] = 0
	decay[1] = 5800
	val[2] = -1
	decay[2] = 5600

	// Use a fixed seed for reproducibility (C uses rand() which is implementation-defined)
	rng := rand.New(rand.NewSource(42))
	for i := 3; i < numValues; i++ {
		val[i] = rng.Intn(15) - 7         // rand()%15-7 -> range [-7, 7]
		decay[i] = rng.Intn(11000) + 5000 // rand()%11000+5000 -> range [5000, 15999]
	}

	// Create encoder and buffer
	buf := make([]byte, dataSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	enc := NewEncoder(1)
	enc.SetRangeEncoder(re)

	// Encode all values
	encodedVal := make([]int, numValues)
	for i := 0; i < numValues; i++ {
		fs := ecLaplaceGetStartFreq(decay[i])
		encodedVal[i] = enc.encodeLaplace(val[i], fs, decay[i])
	}

	// Finalize encoding
	encoded := re.Done()
	t.Logf("Encoded %d values into %d bytes", numValues, len(encoded))

	// Create decoder
	rd := &rangecoding.Decoder{}
	rd.Init(encoded)

	dec := NewDecoder(1)
	dec.SetRangeDecoder(rd)

	// Decode and verify all values
	failures := 0
	for i := 0; i < numValues; i++ {
		fs := ecLaplaceGetStartFreq(decay[i])
		d := dec.decodeLaplace(fs, decay[i])
		if d != encodedVal[i] {
			if failures < 10 {
				t.Errorf("Value %d: got %d instead of %d (original val=%d, decay=%d, fs=%d)",
					i, d, encodedVal[i], val[i], decay[i], fs)
			}
			failures++
		}
	}

	if failures > 0 {
		t.Errorf("Total failures: %d out of %d", failures, numValues)
	} else {
		t.Logf("All %d values decoded correctly", numValues)
	}
}

// TestLaplaceSpecificValues tests specific known values to verify correctness.
func TestLaplaceSpecificValues(t *testing.T) {
	testCases := []struct {
		name  string
		val   int
		decay int
	}{
		{"zero", 0, 6000},
		{"positive_small", 1, 6000},
		{"negative_small", -1, 6000},
		{"positive_medium", 5, 8000},
		{"negative_medium", -5, 8000},
		{"positive_large", 7, 10000},
		{"negative_large", -7, 10000},
		{"zero_low_decay", 0, 5000},
		{"zero_high_decay", 0, 15000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode
			buf := make([]byte, 256)
			re := &rangecoding.Encoder{}
			re.Init(buf)

			enc := NewEncoder(1)
			enc.SetRangeEncoder(re)

			fs := ecLaplaceGetStartFreq(tc.decay)
			encodedVal := enc.encodeLaplace(tc.val, fs, tc.decay)

			encoded := re.Done()

			// Decode
			rd := &rangecoding.Decoder{}
			rd.Init(encoded)

			dec := NewDecoder(1)
			dec.SetRangeDecoder(rd)

			decoded := dec.decodeLaplace(fs, tc.decay)

			if decoded != encodedVal {
				t.Errorf("val=%d, decay=%d, fs=%d: encoded=%d, decoded=%d",
					tc.val, tc.decay, fs, encodedVal, decoded)
			} else {
				t.Logf("val=%d -> encoded=%d -> decoded=%d (decay=%d, fs=%d)",
					tc.val, encodedVal, decoded, tc.decay, fs)
			}
		})
	}
}

// TestLaplaceGetStartFreq verifies the start frequency computation.
func TestLaplaceGetStartFreq(t *testing.T) {
	// Test boundary values
	testCases := []struct {
		decay    int
		expected int // Expected fs based on the formula
	}{
		{5000, 0}, // Will compute and verify
		{6000, 0},
		{8000, 0},
		{10000, 0},
		{15000, 0},
	}

	for _, tc := range testCases {
		fs := ecLaplaceGetStartFreq(tc.decay)
		// Verify fs is within valid range [1, 32768)
		if fs < 1 || fs >= 32768 {
			t.Errorf("decay=%d: fs=%d out of valid range [1, 32768)", tc.decay, fs)
		}
		t.Logf("decay=%d -> fs=%d", tc.decay, fs)
	}
}

// TestLaplaceEdgeCases tests edge cases for Laplace encoding.
func TestLaplaceEdgeCases(t *testing.T) {
	testCases := []struct {
		name  string
		val   int
		decay int
	}{
		{"large_positive", 100, 6000},
		{"large_negative", -100, 6000},
		{"min_decay", 0, 5000},
		{"max_decay", 0, 11456}, // Maximum decay mentioned in libopus
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode
			buf := make([]byte, 1024)
			re := &rangecoding.Encoder{}
			re.Init(buf)

			enc := NewEncoder(1)
			enc.SetRangeEncoder(re)

			fs := ecLaplaceGetStartFreq(tc.decay)
			encodedVal := enc.encodeLaplace(tc.val, fs, tc.decay)

			encoded := re.Done()
			if len(encoded) == 0 {
				t.Fatalf("encoding produced no output")
			}

			// Decode
			rd := &rangecoding.Decoder{}
			rd.Init(encoded)

			dec := NewDecoder(1)
			dec.SetRangeDecoder(rd)

			decoded := dec.decodeLaplace(fs, tc.decay)

			if decoded != encodedVal {
				t.Errorf("val=%d, decay=%d: encoded=%d, decoded=%d",
					tc.val, tc.decay, encodedVal, decoded)
			} else {
				t.Logf("val=%d -> encoded=%d -> decoded=%d (clamped: %v)",
					tc.val, encodedVal, decoded, encodedVal != tc.val)
			}
		})
	}
}

// TestLaplaceMultipleSequences tests encoding multiple sequences to ensure
// proper state management.
func TestLaplaceMultipleSequences(t *testing.T) {
	rng := rand.New(rand.NewSource(123))

	for seq := 0; seq < 10; seq++ {
		numValues := 100
		val := make([]int, numValues)
		decay := make([]int, numValues)

		for i := 0; i < numValues; i++ {
			val[i] = rng.Intn(15) - 7
			decay[i] = rng.Intn(11000) + 5000
		}

		// Encode
		buf := make([]byte, 4096)
		re := &rangecoding.Encoder{}
		re.Init(buf)

		enc := NewEncoder(1)
		enc.SetRangeEncoder(re)

		encodedVal := make([]int, numValues)
		for i := 0; i < numValues; i++ {
			fs := ecLaplaceGetStartFreq(decay[i])
			encodedVal[i] = enc.encodeLaplace(val[i], fs, decay[i])
		}

		encoded := re.Done()

		// Decode
		rd := &rangecoding.Decoder{}
		rd.Init(encoded)

		dec := NewDecoder(1)
		dec.SetRangeDecoder(rd)

		for i := 0; i < numValues; i++ {
			fs := ecLaplaceGetStartFreq(decay[i])
			decoded := dec.decodeLaplace(fs, decay[i])
			if decoded != encodedVal[i] {
				t.Errorf("Sequence %d, value %d: got %d instead of %d",
					seq, i, decoded, encodedVal[i])
			}
		}
	}
}
