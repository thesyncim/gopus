// Package cgo traces header bit encoding between gopus and libopus.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestHeaderBitsEncoding traces what bits get written for different header flags.
func TestHeaderBitsEncoding(t *testing.T) {
	t.Log("=== Header Bits Encoding Trace ===")
	t.Log("")

	// Test different flag combinations
	testCases := []struct {
		name      string
		silence   int
		postfilt  int
		transient int
		intra     int
	}{
		{"All 0s", 0, 0, 0, 0},
		{"Intra only", 0, 0, 0, 1},
		{"Transient only", 0, 0, 1, 0},
		{"Trans + Intra", 0, 0, 1, 1},
	}

	for _, tc := range testCases {
		t.Logf("--- %s: silence=%d, postfilt=%d, transient=%d, intra=%d ---",
			tc.name, tc.silence, tc.postfilt, tc.transient, tc.intra)

		// Encode with gopus
		buf := make([]byte, 256)
		re := &rangecoding.Encoder{}
		re.Init(buf)

		t.Logf("Initial:     rng=0x%08X val=0x%08X tell=%d", re.Range(), re.Val(), re.Tell())

		re.EncodeBit(tc.silence, 15)
		t.Logf("After sil:   rng=0x%08X val=0x%08X tell=%d", re.Range(), re.Val(), re.Tell())

		re.EncodeBit(tc.postfilt, 1)
		t.Logf("After pf:    rng=0x%08X val=0x%08X tell=%d", re.Range(), re.Val(), re.Tell())

		re.EncodeBit(tc.transient, 3)
		t.Logf("After trans: rng=0x%08X val=0x%08X tell=%d", re.Range(), re.Val(), re.Tell())

		re.EncodeBit(tc.intra, 3)
		t.Logf("After intra: rng=0x%08X val=0x%08X tell=%d", re.Range(), re.Val(), re.Tell())

		gopusBytes := re.Done()
		t.Logf("Gopus bytes: %02X", gopusBytes[:minIntHeader(5, len(gopusBytes))])

		// Encode with libopus
		bits := []int{tc.silence, tc.postfilt, tc.transient, tc.intra}
		logps := []int{15, 1, 3, 3}
		libStates, libBytes := TraceBitSequence(bits, logps)

		t.Logf("Libopus bytes: %02X", libBytes[:minIntHeader(5, len(libBytes))])

		// Compare
		if len(gopusBytes) > 0 && len(libBytes) > 0 {
			if gopusBytes[0] == libBytes[0] {
				t.Log("First byte: MATCH")
			} else {
				t.Logf("First byte DIFFERS: gopus=0x%02X libopus=0x%02X", gopusBytes[0], libBytes[0])
			}
		}

		// Show libopus states
		if libStates != nil && len(libStates) > 4 {
			t.Logf("Libopus after intra: rng=0x%08X val=0x%08X tell=%d",
				libStates[4].Rng, libStates[4].Val, libStates[4].Tell)
		}

		t.Log("")
	}
}

func minIntHeader(a, b int) int {
	if a < b {
		return a
	}
	return b
}
