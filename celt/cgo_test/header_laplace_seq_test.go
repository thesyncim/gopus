//go:build trace
// +build trace

// Package cgo tests header + Laplace sequence encoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestHeaderLaplaceSequence tests header + Laplace encoding step by step.
func TestHeaderLaplaceSequence(t *testing.T) {
	t.Log("=== Header + Laplace Sequence Test ===")
	t.Log("")

	// Header flags (matching actual gopus encoding)
	// silence=0, postfilter=0, transient=1, intra=0
	headerBits := []int{0, 0, 1, 0}
	headerLogps := []int{15, 1, 3, 3}

	// Laplace values (from TestActualQICompare)
	qiValues := []int{3, 3, 2, -2, -1, -2, -2, 1, 0, -1}

	// Get fs/decay from probability model
	probModel := celt.GetEProbModel()
	prob := probModel[3][0] // LM=3, inter mode

	fsValues := make([]int, len(qiValues))
	decayValues := make([]int, len(qiValues))
	for i := range qiValues {
		pi := 2 * i
		if pi > 40 {
			pi = 40
		}
		fsValues[i] = int(prob[pi]) << 7
		decayValues[i] = int(prob[pi+1]) << 6
	}

	// Encode with gopus: header + Laplace
	t.Log("=== Gopus Encoding ===")
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	t.Logf("Initial: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode header
	for i, bit := range headerBits {
		re.EncodeBit(bit, uint(headerLogps[i]))
	}
	t.Logf("After header: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode Laplace values
	enc := celt.NewEncoder(1)
	enc.SetRangeEncoder(re)

	for i, qi := range qiValues {
		_ = enc.TestEncodeLaplace(qi, fsValues[i], decayValues[i])
		if i < 5 {
			t.Logf("After band %d (qi=%d): rng=0x%08X, val=0x%08X, tell=%d",
				i, qi, re.Range(), re.Val(), re.Tell())
		}
	}

	gopusBytes := re.Done()
	t.Logf("Gopus bytes: %02X", gopusBytes[:minIntHLS(10, len(gopusBytes))])

	// Encode with libopus using TraceHeaderPlusLaplace
	t.Log("")
	t.Log("=== Libopus Encoding ===")
	libStates, libQIs, libBytes := TraceHeaderPlusLaplace(
		headerBits, headerLogps, qiValues, fsValues, decayValues)

	if libStates != nil && len(libStates) > 4 {
		t.Logf("After header: rng=0x%08X, val=0x%08X, tell=%d",
			libStates[4].Rng, libStates[4].Val, libStates[4].Tell)
	}

	for i := 0; i < 5 && i < len(libStates)-5; i++ {
		idx := 5 + i // After header (4 flags) + initial state
		t.Logf("After band %d: rng=0x%08X, val=0x%08X, tell=%d",
			i, libStates[idx].Rng, libStates[idx].Val, libStates[idx].Tell)
	}

	t.Logf("Libopus bytes: %02X", libBytes[:minIntHLS(10, len(libBytes))])

	// Compare
	t.Log("")
	t.Log("=== Comparison ===")

	// Check if QI values were modified
	allQIsMatch := true
	for i := range qiValues {
		if i < len(libQIs) && qiValues[i] != libQIs[i] {
			t.Logf("Band %d: input qi=%d, output qi=%d (MODIFIED)", i, qiValues[i], libQIs[i])
			allQIsMatch = false
		}
	}
	if allQIsMatch {
		t.Log("All QI values preserved (not clamped)")
	}

	// Compare bytes
	t.Log("")
	t.Log("Byte comparison:")
	for i := 0; i < 10 && i < len(gopusBytes) && i < len(libBytes); i++ {
		match := "MATCH"
		if gopusBytes[i] != libBytes[i] {
			match = "DIFFER"
		}
		t.Logf("  [%d]: gopus=0x%02X (%08b), libopus=0x%02X (%08b) - %s",
			i, gopusBytes[i], gopusBytes[i], libBytes[i], libBytes[i], match)
	}

	// Determine where divergence starts
	t.Log("")
	divergeIdx := -1
	for i := 0; i < len(gopusBytes) && i < len(libBytes); i++ {
		if gopusBytes[i] != libBytes[i] {
			divergeIdx = i
			break
		}
	}
	if divergeIdx >= 0 {
		t.Logf("First divergence at byte %d", divergeIdx)
	} else if len(gopusBytes) == len(libBytes) {
		t.Log("All bytes MATCH!")
	} else {
		t.Logf("Lengths differ: gopus=%d, libopus=%d", len(gopusBytes), len(libBytes))
	}
}

func minIntHLS(a, b int) int {
	if a < b {
		return a
	}
	return b
}
