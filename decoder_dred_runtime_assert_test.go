//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import "testing"

func assertDecoderDREDRuntimeLoadedForTest(t testing.TB, dec *Decoder, label string) {
	t.Helper()
	state := requireDecoderDREDState(t, dec)
	if !state.dredAnalysis.Loaded() || !state.dredPredictor.Loaded() || !state.dredFARGAN.Loaded() {
		t.Fatalf("decoder runtime models not loaded after %s", label)
	}
}
