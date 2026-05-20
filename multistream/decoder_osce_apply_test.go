//go:build gopus_extra_controls
// +build gopus_extra_controls

package multistream

import "testing"

func TestStreamOSCEFloatToInt16MatchesLibopusScaleOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float32
		want int16
	}{
		{name: "positive clamp", in: 1.5, want: 32767},
		{name: "negative clamp", in: -1.5, want: -32767},
		{name: "negative full scale", in: -1.0, want: -32767},
		{name: "half tie to even", in: float32(0.5 / 32768.0), want: 0},
		{name: "one point five tie to even", in: float32(1.5 / 32768.0), want: 2},
		{name: "two point five tie to even", in: float32(2.5 / 32768.0), want: 2},
		{name: "negative one point five tie to even", in: float32(-1.5 / 32768.0), want: -2},
	} {
		if got := streamOSCEFloatToInt16(tc.in); got != tc.want {
			t.Fatalf("%s: streamOSCEFloatToInt16(%g)=%d want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestStreamOSCELACEComplexityMode(t *testing.T) {
	for _, tc := range []struct {
		complexity int
		want       streamOSCELACEMode
	}{
		{complexity: 5, want: streamOSCELACEModeNone},
		{complexity: 6, want: streamOSCELACEModeLACE},
		{complexity: 7, want: streamOSCELACEModeNoLACE},
		{complexity: 10, want: streamOSCELACEModeNoLACE},
	} {
		if got := pickStreamOSCELACEMode(tc.complexity); got != tc.want {
			t.Fatalf("pickStreamOSCELACEMode(%d)=%v want %v", tc.complexity, got, tc.want)
		}
	}
}
