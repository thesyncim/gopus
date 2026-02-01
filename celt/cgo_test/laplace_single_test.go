//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests single Laplace encoding comparison.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestSingleLaplaceEncoding compares single Laplace encodings between gopus and libopus.
func TestSingleLaplaceEncoding(t *testing.T) {
	t.Log("=== Single Laplace Encoding Comparison ===")
	t.Log("")

	// Test parameters for LM=3 (960 sample frame), inter mode
	probModel := celt.GetEProbModel()
	prob := probModel[3][0] // LM=3, inter mode

	// Test cases: (qi, band)
	testCases := []struct {
		band int
		qi   int
	}{
		{0, 3},
		{1, 3},
		{2, 2},
		{3, -2},
		{4, -1},
		{5, -2},
		{6, -2},
		{7, 1},
	}

	t.Log("Testing individual Laplace encodings:")
	t.Log("Band | QI | fs     | decay  | Gopus state              | Libopus state")
	t.Log("-----+----+--------+--------+--------------------------+---------------------------")

	for _, tc := range testCases {
		pi := 2 * tc.band
		if pi > 40 {
			pi = 40
		}
		fs := int(prob[pi]) << 7
		decay := int(prob[pi+1]) << 6

		// Gopus encoding
		buf := make([]byte, 256)
		re := &rangecoding.Encoder{}
		re.Init(buf)

		enc := celt.NewEncoder(1)
		enc.SetRangeEncoder(re)
		gopusOutQI := enc.TestEncodeLaplace(tc.qi, fs, decay)

		gopusBytes := re.Done()
		gopusRng := re.Range()
		gopusTell := re.Tell()

		// Libopus encoding
		libBytes, libQI, err := EncodeLaplace(tc.qi, fs, decay)
		if err != nil {
			t.Fatalf("libopus EncodeLaplace failed: %v", err)
		}

		// Compare
		match := "MATCH"
		if len(gopusBytes) > 0 && len(libBytes) > 0 && gopusBytes[0] != libBytes[0] {
			match = "DIFFER"
		}

		t.Logf("%4d | %2d | %6d | %6d | gopus: rng=0x%08X tell=%2d outQI=%d | lib: byte0=0x%02X outQI=%d | %s",
			tc.band, tc.qi, fs, decay, gopusRng, gopusTell, gopusOutQI,
			libBytes[0], libQI, match)

		if match == "DIFFER" {
			t.Logf("       Gopus  byte0=0x%02X", gopusBytes[0])
			t.Logf("       Libopus byte0=0x%02X", libBytes[0])
		}
	}

	// Test a sequence of Laplace values (simulating coarse energy)
	t.Log("")
	t.Log("=== Laplace Sequence Test ===")
	t.Log("Encoding all 8 bands in sequence")

	// Gopus sequence
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	enc := celt.NewEncoder(1)
	enc.SetRangeEncoder(re)

	for _, tc := range testCases {
		pi := 2 * tc.band
		if pi > 40 {
			pi = 40
		}
		fs := int(prob[pi]) << 7
		decay := int(prob[pi+1]) << 6
		_ = enc.TestEncodeLaplace(tc.qi, fs, decay)
	}

	gopusFinalBytes := re.Done()
	t.Logf("Gopus sequence bytes: %02X", gopusFinalBytes[:minIntLaplaceSingle(10, len(gopusFinalBytes))])

	// Libopus sequence
	qiValues := make([]int, len(testCases))
	fsValues := make([]int, len(testCases))
	decayValues := make([]int, len(testCases))

	for i, tc := range testCases {
		qiValues[i] = tc.qi
		pi := 2 * tc.band
		if pi > 40 {
			pi = 40
		}
		fsValues[i] = int(prob[pi]) << 7
		decayValues[i] = int(prob[pi+1]) << 6
	}

	libBytes, _, err := EncodeLaplaceSequence(qiValues, fsValues, decayValues)
	if err != nil {
		t.Fatalf("libopus EncodeLaplaceSequence failed: %v", err)
	}
	t.Logf("Libopus sequence bytes: %02X", libBytes[:minIntLaplaceSingle(10, len(libBytes))])

	// Compare
	t.Log("")
	t.Log("Byte comparison:")
	for i := 0; i < 10 && i < len(gopusFinalBytes) && i < len(libBytes); i++ {
		match := "MATCH"
		if gopusFinalBytes[i] != libBytes[i] {
			match = "DIFFER"
		}
		t.Logf("  [%d]: gopus=0x%02X, libopus=0x%02X - %s", i, gopusFinalBytes[i], libBytes[i], match)
	}
}

func minIntLaplaceSingle(a, b int) int {
	if a < b {
		return a
	}
	return b
}
