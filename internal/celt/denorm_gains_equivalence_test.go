package celt

import (
	"math"
	"math/rand"
	"testing"
)

func TestDenormalizeBandGainsMatchScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0xde7))
	specials := []float32{0, float32(math.Copysign(0, -1)), 32, 31.999998, 32.5, -50, -50.5, -51, -60, 1e4, -1e4, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))}
	for iter := 0; iter < 20000; iter++ {
		end := 1 + rng.Intn(len(eMeans))
		start := rng.Intn(end)
		energies := make([]celtGLog, end+rng.Intn(4))
		for i := range energies {
			switch rng.Intn(10) {
			case 0:
				energies[i] = specials[rng.Intn(len(specials))]
			case 1:
				energies[i] = (rng.Float32()*2 - 1) * 200
			default:
				energies[i] = (rng.Float32()*2 - 1) * 30
			}
		}
		got := make([]float32, end)
		want := make([]float32, end)
		denormalizeBandGains(got, energies, start, end)
		denormalizeBandGainsScalar(want, energies, start, end)
		for b := start; b < end; b++ {
			if math.Float32bits(got[b]) != math.Float32bits(want[b]) {
				t.Fatalf("iter %d band %d energy %v: gain %v (%#x), want %v (%#x)", iter, b, energies[b], got[b], math.Float32bits(got[b]), want[b], math.Float32bits(want[b]))
			}
		}
	}
}
