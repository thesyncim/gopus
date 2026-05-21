package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQualityFloat32ToPCM16UsesOpusRounding(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		-2, -1.5, -1, -0.9999695,
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		0.9999695, 1, 1.5, 2,
	}
	want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeFloat2Int16, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	got := float32ToPCM16(samples)
	for i, sample := range samples {
		if got[i] != want[i] {
			t.Fatalf("float32ToPCM16[%d](%0.10g)=%d want %d", i, sample, got[i], want[i])
		}
		if gotQuantized, wantQuantized := quantizeTo16(sample), float32(want[i])/32768.0; gotQuantized != wantQuantized {
			t.Fatalf("quantizeTo16(%0.10g)=%0.10g want %0.10g", sample, gotQuantized, wantQuantized)
		}
	}
}
