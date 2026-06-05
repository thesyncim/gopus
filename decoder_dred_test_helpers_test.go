//go:build gopus_dred || gopus_osce

package gopus

import (
	"math"
	"testing"
)

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

func assertDecoderDREDRuntimeLoadedForTest(t testing.TB, dec *Decoder, label string) {
	t.Helper()
	state := requireDecoderDREDState(t, dec)
	if !state.dredAnalysis.Loaded() || !state.dredPredictor.Loaded() || !state.dredFARGAN.Loaded() {
		t.Fatalf("decoder runtime models not loaded after %s", label)
	}
}

func setDecoderComplexityForLibopusDREDParityTest(t testing.TB, dec *Decoder) {
	t.Helper()
	if err := dec.SetComplexity(10); err != nil {
		t.Fatalf("SetComplexity(10) error: %v", err)
	}
}

func assertFloat32BitsEqual(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%g want %g", label, i, got[i], want[i])
		}
	}
}
