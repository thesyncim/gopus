//go:build gopus_osce

package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestStreamOSCEFloatToInt16MatchesLibopusOutputScaleCGrid(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := make([]float32, 0, 2*65540)
	for raw := -32770; raw <= 32769; raw++ {
		samples = append(samples, float32(raw)*(1.0/32768.0))
		samples = append(samples, float32(float64(raw)+0.5)*(1.0/32768.0))
	}
	want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeOSCEOutputScale, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	for i, sample := range samples {
		if got := streamOSCEFloatToInt16(sample); got != want[i] {
			t.Fatalf("streamOSCEFloatToInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}
