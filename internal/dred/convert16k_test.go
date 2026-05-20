package dred

import (
	"math"
	"testing"
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
