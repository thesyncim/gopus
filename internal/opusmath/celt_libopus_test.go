package opusmath

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestCELTLog2MatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := celtLog2OracleSamples()
	want, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeLog2, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, sample := range samples {
		got := CeltLog2(sample)
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("CeltLog2(%g)=%08x(%g) want %08x(%g)",
				sample,
				math.Float32bits(got), got,
				math.Float32bits(want[i]), want[i],
			)
		}
	}
}

func TestCELTExp2MatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := celtExp2OracleSamples()
	want, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeExp2, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, sample := range samples {
		got := CeltExp2(sample)
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("CeltExp2(%g)=%08x(%g) want %08x(%g)",
				sample,
				math.Float32bits(got), got,
				math.Float32bits(want[i]), want[i],
			)
		}
	}
}

func TestCELTISqrt32MatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	inputs := celtISqrt32OracleInputs()
	want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeISqrt32, len(inputs), inputs)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, x := range inputs {
		got := ISqrt32(x)
		if got != want[i] {
			t.Fatalf("ISqrt32(%d)=%d want %d", x, got, want[i])
		}
	}
}

func celtLog2OracleSamples() []float32 {
	samples := []float32{
		math.SmallestNonzeroFloat32,
		1e-30, 1e-20, 1e-10, 1e-5, 0.03125,
		0.5, 0.75, 0.99999994, 1, 1.0000001,
		1.125, 1.25, 1.5, 1.875, 2, 3, 8, 1024,
	}
	for exp := int32(-12); exp <= 12; exp++ {
		for mant := uint32(0); mant < 8; mant++ {
			bits := uint32(exp+127)<<23 | mant<<20 | 0x12345
			samples = append(samples, math.Float32frombits(bits))
		}
	}
	return samples
}

func celtISqrt32OracleInputs() []uint32 {
	inputs := []uint32{
		0, 1, 2, 3, 4, 15, 16, 17,
		(1 << 16) - 1, 1 << 16, (1 << 16) + 1,
		(1 << 24) - 1, 1 << 24, (1 << 24) + 1,
		^uint32(0) - 2, ^uint32(0) - 1, ^uint32(0),
	}
	for i := uint32(1); i < 65536; i += 257 {
		inputs = append(inputs, i*i, i*i+1)
		if i > 0 {
			inputs = append(inputs, i*i-1)
		}
	}
	return inputs
}

func celtExp2OracleSamples() []float32 {
	samples := []float32{
		-60, -51, -50.5, -50, -24, -10,
		-1.75, -1.5, -1.25, -1, -0.75, -0.5, -0.25,
		0, 0.25, 0.5, 0.75, 1, 1.25, 2, 5, 10, 24,
	}
	for integer := int32(-12); integer <= 12; integer++ {
		for _, frac := range []float32{0, 0.0625, 0.125, 0.33325195, 0.5, 0.875, 0.99902344} {
			samples = append(samples, float32(integer)+frac)
		}
	}
	return samples
}
