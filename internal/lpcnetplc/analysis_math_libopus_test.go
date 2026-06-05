package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestLPCNetLog10fUsesLibopusCELTLog2Oracle(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		math.SmallestNonzeroFloat32,
		1e-30, 1e-20, 1e-10, 1e-5, 0.03125,
		0.5, 0.75, 0.99999994, 1, 1.0000001,
		1.125, 1.25, 1.5, 1.875, 2, 3, 8, 1024,
	}
	for exp := int32(-12); exp <= 12; exp++ {
		for mant := range uint32(8) {
			bits := uint32(exp+127)<<23 | mant<<20 | 0x54321
			samples = append(samples, math.Float32frombits(bits))
		}
	}
	wantLog2, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeLog2, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, sample := range samples {
		want := float32(0.3010299957) * wantLog2[i]
		got := log10f(sample)
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("log10f(%g)=%08x(%g) want %08x(%g)",
				sample,
				math.Float32bits(got), got,
				math.Float32bits(want), want,
			)
		}
	}
}
