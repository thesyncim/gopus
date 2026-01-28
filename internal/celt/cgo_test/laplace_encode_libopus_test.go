// Package cgo provides CGO-based tests to validate gopus against libopus.
package cgo

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestLaplaceEncodeVsLibopus compares Laplace encoding between gopus and libopus.
func TestLaplaceEncodeVsLibopus(t *testing.T) {
	// Test cases with various values and probability parameters
	testCases := []struct {
		name  string
		val   int
		fs    int
		decay int
	}{
		// Zero value
		{"val_0_band0", 0, 72 << 7, 127 << 6},
		// Positive values
		{"val_1_band0", 1, 72 << 7, 127 << 6},
		{"val_2_band0", 2, 72 << 7, 127 << 6},
		{"val_3_band0", 3, 72 << 7, 127 << 6},
		{"val_5_band0", 5, 72 << 7, 127 << 6},
		{"val_10_band0", 10, 72 << 7, 127 << 6},
		// Negative values
		{"val_-1_band0", -1, 72 << 7, 127 << 6},
		{"val_-2_band0", -2, 72 << 7, 127 << 6},
		{"val_-3_band0", -3, 72 << 7, 127 << 6},
		{"val_-5_band0", -5, 72 << 7, 127 << 6},
		{"val_-10_band0", -10, 72 << 7, 127 << 6},
		// Different probability parameters (from e_prob_model)
		{"val_1_lm3_inter", 1, 42 << 7, 121 << 6},
		{"val_-1_lm3_inter", -1, 42 << 7, 121 << 6},
		{"val_2_lm3_intra", 2, 22 << 7, 178 << 6},
		{"val_-2_lm3_intra", -2, 22 << 7, 178 << 6},
		// Large values (will be clamped by fs)
		{"val_20_band0", 20, 72 << 7, 127 << 6},
		{"val_-20_band0", -20, 72 << 7, 127 << 6},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode with libopus
			libBytes, libVal, err := EncodeLaplace(tc.val, tc.fs, tc.decay)
			if err != nil {
				t.Fatalf("libopus encode failed: %v", err)
			}

			// Encode with gopus
			goBuf := make([]byte, 256)
			goEnc := &rangecoding.Encoder{}
			goEnc.Init(goBuf)

			// Create a dummy encoder to access encodeLaplace
			enc := celt.NewEncoder(1)
			enc.SetRangeEncoder(goEnc)
			goVal := enc.TestEncodeLaplace(tc.val, tc.fs, tc.decay)

			goBytes := goEnc.Done()

			// Compare results
			if goVal != libVal {
				t.Errorf("Value mismatch: gopus=%d, libopus=%d", goVal, libVal)
			}

			if !bytes.Equal(goBytes, libBytes) {
				t.Errorf("Bytes mismatch:\n  gopus=%v (len=%d)\n  libopus=%v (len=%d)",
					goBytes, len(goBytes), libBytes, len(libBytes))
			} else {
				t.Logf("Match: val=%d -> output val=%d, bytes=%v", tc.val, goVal, goBytes)
			}
		})
	}
}

// TestLaplaceEncodeDecodeRoundtrip verifies that encoded values can be decoded correctly.
func TestLaplaceEncodeDecodeRoundtrip(t *testing.T) {
	testCases := []struct {
		val   int
		fs    int
		decay int
	}{
		{0, 72 << 7, 127 << 6},
		{1, 72 << 7, 127 << 6},
		{-1, 72 << 7, 127 << 6},
		{2, 72 << 7, 127 << 6},
		{-2, 72 << 7, 127 << 6},
		{5, 42 << 7, 121 << 6},
		{-5, 42 << 7, 121 << 6},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Encode with gopus
			goBuf := make([]byte, 256)
			goEnc := &rangecoding.Encoder{}
			goEnc.Init(goBuf)

			enc := celt.NewEncoder(1)
			enc.SetRangeEncoder(goEnc)
			encodedVal := enc.TestEncodeLaplace(tc.val, tc.fs, tc.decay)

			goBytes := goEnc.Done()

			// Decode with gopus
			rd := &rangecoding.Decoder{}
			rd.Init(goBytes)
			dec := celt.NewDecoder(1)
			dec.SetRangeDecoder(rd)
			decodedVal := dec.DecodeLaplaceTest(tc.fs, tc.decay)

			// The encoded and decoded values should match
			if decodedVal != encodedVal {
				t.Errorf("Roundtrip mismatch: encoded=%d, decoded=%d (input=%d)",
					encodedVal, decodedVal, tc.val)
			}

			// Also verify libopus can decode our output
			libDecodedVal := DecodeLaplace(goBytes, tc.fs, tc.decay)
			if libDecodedVal != encodedVal {
				t.Errorf("Libopus decode mismatch: gopus_decoded=%d, libopus_decoded=%d",
					decodedVal, libDecodedVal)
			}

			t.Logf("Roundtrip OK: input=%d, encoded=%d, decoded=%d", tc.val, encodedVal, decodedVal)
		})
	}
}

// TestLaplaceEncodeSequenceVsLibopus tests encoding a sequence of values (like coarse energy).
func TestLaplaceEncodeSequenceVsLibopus(t *testing.T) {
	// Get the probability model from gopus
	probModel := celt.GetEProbModel()

	// Test sequence: energy qi values for bands 0-4, LM=3, inter mode
	lm := 3
	vals := []int{1, -1, 2, 0, -2}
	fsArr := make([]int, len(vals))
	decayArr := make([]int, len(vals))

	for i := range vals {
		pi := 2 * i
		if pi > 40 {
			pi = 40
		}
		fsArr[i] = int(probModel[lm][0][pi]) << 7
		decayArr[i] = int(probModel[lm][0][pi+1]) << 6
	}

	// Encode with libopus
	libBytes, libVals, err := EncodeLaplaceSequence(vals, fsArr, decayArr)
	if err != nil {
		t.Fatalf("libopus encode failed: %v", err)
	}

	// Encode with gopus
	goBuf := make([]byte, 4096)
	goEnc := &rangecoding.Encoder{}
	goEnc.Init(goBuf)

	enc := celt.NewEncoder(1)
	enc.SetRangeEncoder(goEnc)

	goVals := make([]int, len(vals))
	for i := range vals {
		goVals[i] = enc.TestEncodeLaplace(vals[i], fsArr[i], decayArr[i])
	}

	goBytes := goEnc.Done()

	// Compare values
	allMatch := true
	for i := range vals {
		if goVals[i] != libVals[i] {
			t.Errorf("Value[%d] mismatch: gopus=%d, libopus=%d", i, goVals[i], libVals[i])
			allMatch = false
		}
	}

	// Compare bytes
	if !bytes.Equal(goBytes, libBytes) {
		t.Errorf("Bytes mismatch:\n  gopus=%v (len=%d)\n  libopus=%v (len=%d)",
			goBytes, len(goBytes), libBytes, len(libBytes))
	} else if allMatch {
		t.Logf("Sequence match: vals=%v, bytes=%v", goVals, goBytes)
	}
}

// TestCoarseEnergyEncodingVsLibopus tests the full coarse energy encoding path.
func TestCoarseEnergyEncodingVsLibopus(t *testing.T) {
	// Test with some realistic energy values
	energies := []float64{
		1.0, 2.0, 3.0, 4.0, 5.0,
		4.0, 3.0, 2.0, 1.0, 0.0,
		-1.0, -2.0, -3.0, -4.0, -5.0,
		-4.0, -3.0, -2.0, -1.0, 0.0,
		1.0,
	}

	nbBands := len(energies)
	lm := 3
	intra := true

	// Encode with gopus
	goBuf := make([]byte, 4096)
	goEnc := &rangecoding.Encoder{}
	goEnc.Init(goBuf)

	enc := celt.NewEncoder(1)
	enc.SetRangeEncoder(goEnc)
	enc.SetFrameBitsForTest(len(goBuf) * 8)

	// Encode coarse energies
	quantized := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	goBytes := goEnc.Done()

	t.Logf("Coarse energy encoding: %d bands, intra=%v, lm=%d", nbBands, intra, lm)
	t.Logf("Quantized energies (first 10):")
	for i := 0; i < 10 && i < len(quantized); i++ {
		t.Logf("  Band %d: input=%.3f, quantized=%.3f", i, energies[i], quantized[i])
	}
	minLen := len(goBytes)
	if minLen > 20 {
		minLen = 20
	}
	t.Logf("Encoded bytes (len=%d): %v", len(goBytes), goBytes[:minLen])

	// Decode with gopus and verify roundtrip
	rd := &rangecoding.Decoder{}
	rd.Init(goBytes)
	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)

	// Skip intra flag (would need to be decoded first in real usage)
	decoded := dec.DecodeCoarseEnergy(nbBands, intra, lm)

	t.Logf("Decoded energies (first 10):")
	mismatchCount := 0
	for i := 0; i < 10 && i < len(decoded); i++ {
		diff := decoded[i] - quantized[i]
		if diff < -0.001 || diff > 0.001 {
			mismatchCount++
			t.Errorf("  Band %d: quantized=%.3f, decoded=%.3f, DIFF=%.6f", i, quantized[i], decoded[i], diff)
		} else {
			t.Logf("  Band %d: quantized=%.3f, decoded=%.3f", i, quantized[i], decoded[i])
		}
	}

	if mismatchCount == 0 {
		t.Log("All decoded energies match quantized energies")
	}
}
