package gopus

import (
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/opusmath"
)

func TestConvertFloat32ToInt16Unit(t *testing.T) {
	src := []float32{-1, -0.75, -0.5, -1.5 / 32768, -0.5 / 32768, 0, 0.5 / 32768, 1.5 / 32768, 0.5, 0.75, 0.99999, 1}
	dst := make([]int16, len(src))
	ok := convertFloat32ToInt16Unit(dst, src, len(src))
	if runtime.GOARCH != "arm64" || testPuregoBuild {
		if ok {
			t.Fatal("default conversion unexpectedly handled the vector")
		}
		return
	}
	if !ok {
		t.Fatal("arm64 conversion rejected in-range samples")
	}
	for i, v := range src {
		if want := float32ToInt16(v); dst[i] != want {
			t.Fatalf("dst[%d] = %d, want %d", i, dst[i], want)
		}
	}

	outOfRange := []float32{0, 1.01}
	if convertFloat32ToInt16Unit(make([]int16, len(outOfRange)), outOfRange, len(outOfRange)) {
		t.Fatal("arm64 conversion accepted out-of-range samples")
	}
}

func TestFloat32ToInt16OpusRoundingFixture(t *testing.T) {
	tests := []struct {
		name string
		in   float32
		want int16
	}{
		{name: "-one", in: -1, want: -32768},
		{name: "negative clamp below minus one", in: math.Nextafter32(-1, float32(math.Inf(-1))), want: -32768},
		{name: "negative clamp just above minus one", in: math.Nextafter32(-1, 0), want: -32768},
		{name: "-1.5/32768 tie to even", in: float32(-1.5 / 32768.0), want: -2},
		{name: "-0.5/32768 tie to even", in: float32(-0.5 / 32768.0), want: 0},
		{name: "zero", in: 0, want: 0},
		{name: "0.5/32768 tie to even", in: float32(0.5 / 32768.0), want: 0},
		{name: "1.5/32768 tie to even", in: float32(1.5 / 32768.0), want: 2},
		{name: "positive max exact", in: float32(32767.0 / 32768.0), want: 32767},
		{name: "positive clamp at one", in: 1, want: 32767},
		{name: "positive clamp above one", in: math.Nextafter32(1, float32(math.Inf(1))), want: 32767},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := float32ToInt16(tc.in); got != tc.want {
				t.Fatalf("float32ToInt16(%0.10g) = %d, want %d", tc.in, got, tc.want)
			}
			if got := opusmath.Float32ToInt16(tc.in); got != tc.want {
				t.Fatalf("opusmath.Float32ToInt16(%0.10g) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestFloat32ToInt16OpusRoundingBitNeighbors(t *testing.T) {
	tests := []struct {
		name string
		base float32
		want [3]int16
	}{
		{name: "-1.5/32768", base: float32(-1.5 / 32768.0), want: [3]int16{-2, -2, -1}},
		{name: "-0.5/32768", base: float32(-0.5 / 32768.0), want: [3]int16{-1, 0, 0}},
		{name: "0.5/32768", base: float32(0.5 / 32768.0), want: [3]int16{0, 0, 1}},
		{name: "1.5/32768", base: float32(1.5 / 32768.0), want: [3]int16{1, 2, 2}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			samples := []float32{
				math.Nextafter32(tc.base, float32(math.Inf(-1))),
				tc.base,
				math.Nextafter32(tc.base, float32(math.Inf(1))),
			}
			for i, sample := range samples {
				if got := float32ToInt16(sample); got != tc.want[i] {
					t.Fatalf("float32ToInt16 neighbor %d (%0.10g) = %d, want %d", i, sample, got, tc.want[i])
				}
				if got := opusmath.Float32ToInt16(sample); got != tc.want[i] {
					t.Fatalf("opusmath.Float32ToInt16 neighbor %d (%0.10g) = %d, want %d", i, sample, got, tc.want[i])
				}
			}
		})
	}
}
