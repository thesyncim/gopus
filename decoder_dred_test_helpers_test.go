//go:build gopus_dred || gopus_extra_controls

package gopus

import "testing"

func requireDecoderDREDState(t testing.TB, dec *Decoder) *decoderDREDState {
	t.Helper()
	if dec == nil {
		t.Fatal("decoder is nil")
	}
	s := dec.dredState()
	if s == nil {
		t.Fatal("decoder DRED sidecar is nil")
	}
	return s
}
