package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDynallocImportanceRoundingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		-0.25,
		0,
		math.SmallestNonzeroFloat32,
		0.125,
		1,
		2,
		3.5,
		4,
		math.Nextafter32(4, 5),
	}
	for n := 1; n < 208; n += 7 {
		threshold := math.Log2((float64(n) + 0.5) / 13.0)
		if threshold < -4 || threshold > 4.25 {
			continue
		}
		f := float32(threshold)
		samples = append(samples,
			math.Nextafter32(f, float32(math.Inf(-1))),
			f,
			math.Nextafter32(f, float32(math.Inf(1))),
		)
	}

	words := make([]uint32, len(samples))
	for i, sample := range samples {
		words[i] = math.Float32bits(sample)
	}
	want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeDynallocImportance, len(samples), words)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt dynalloc importance", err)
	}
	for i, sample := range samples {
		got := dynallocImportanceFromFollower(sample)
		if got != int(int32(want[i])) {
			t.Fatalf("importance(%08x %.10g)=%d want %d", math.Float32bits(sample), sample, got, int(int32(want[i])))
		}
	}
}
