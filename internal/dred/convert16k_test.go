package dred

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDREDFloatToInt16UsesSharedOpusRounding(t *testing.T) {
	tests := []struct {
		name string
		in   float32
		want int16
	}{
		{name: "-one", in: -1, want: -32768},
		{name: "below minus one", in: math.Nextafter32(-1, float32(math.Inf(-1))), want: -32768},
		{name: "-1.5/32768", in: float32(-1.5 / 32768.0), want: -2},
		{name: "-0.5/32768", in: float32(-0.5 / 32768.0), want: 0},
		{name: "0.5/32768", in: float32(0.5 / 32768.0), want: 0},
		{name: "1.5/32768", in: float32(1.5 / 32768.0), want: 2},
		{name: "max exact", in: float32(32767.0 / 32768.0), want: 32767},
		{name: "one clamps", in: 1, want: 32767},
		{name: "above one", in: math.Nextafter32(1, float32(math.Inf(1))), want: 32767},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dredFloatToInt16(tc.in); got != tc.want {
				t.Fatalf("dredFloatToInt16(%0.10g)=%d want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestDREDFloatToInt16MatchesLibopusFLOAT2INT16(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		math.Nextafter32(-1, float32(math.Inf(-1))),
		-1,
		float32(-32767.5 / 32768.0),
		float32(-1235.5 / 32768.0),
		float32(-1234.5 / 32768.0),
		float32(-3.5 / 32768.0),
		float32(-2.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(2.5 / 32768.0),
		float32(3.5 / 32768.0),
		float32(1234.5 / 32768.0),
		float32(1235.5 / 32768.0),
		float32(32766.5 / 32768.0),
		float32(32767.5 / 32768.0),
		1,
		math.Nextafter32(1, float32(math.Inf(1))),
	}
	for raw := -64; raw <= 64; raw++ {
		sample := float32(raw) / 32768.0
		samples = append(samples,
			math.Nextafter32(sample, float32(math.Inf(-1))),
			sample,
			math.Nextafter32(sample, float32(math.Inf(1))),
		)
	}
	want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeFloat2Int16, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "dred float2int16", err)
	}
	for i, sample := range samples {
		if got := dredFloatToInt16(sample); got != want[i] {
			t.Fatalf("dredFloatToInt16(sample[%d]=%0.10g)=%d want %d", i, sample, got, want[i])
		}
	}
}
