//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import "testing"

func setDecoderComplexityForLibopusDREDParityTest(t testing.TB, dec *Decoder) {
	t.Helper()
	if err := dec.SetComplexity(10); err != nil {
		t.Fatalf("SetComplexity(10) error: %v", err)
	}
}
