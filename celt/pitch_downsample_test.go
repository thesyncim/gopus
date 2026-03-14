package celt

import (
	"math"
	"math/rand"
	"testing"
)

func TestPitchDownsampleMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	tests := []struct {
		name     string
		channels int
	}{
		{name: "mono", channels: 1},
		{name: "stereo", channels: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for trial := 0; trial < 200; trial++ {
				length := rng.Intn(300) + 8
				xLen := 2 * length
				if tt.channels == 2 {
					xLen *= 2
				}
				x := make([]float64, xLen)
				for i := range x {
					x[i] = rng.Float64()*2 - 1
				}
				got := make([]float64, length)
				want := make([]float64, length)

				pitchDownsample(x, got, length, tt.channels, 2)
				pitchDownsampleLegacy(x, want, length, tt.channels, 2)

				for i := range want {
					if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
						t.Fatalf("trial %d sample %d bits=%#x want=%#x", trial, i, math.Float64bits(got[i]), math.Float64bits(want[i]))
					}
				}
			}
		})
	}
}
