//go:build amd64 && goexperiment.simd && !nosimd && !gopus_fixed_point

package celt

import (
	"math"
	"testing"

	"simd/archsimd"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestCELTExp2x4MatchesLibopus(t *testing.T) {
	if denormTargetV3FMA {
		// A GOAMD64=v3 test run is an audit lane; missing its required hardware
		// features is a configuration error, not a reason to skip the oracle.
		if !archsimd.X86.AVX() || !archsimd.X86.FMA() {
			t.Fatalf("GOAMD64=v3 exp2 oracle requires AVX and FMA hardware")
		}
	} else if !archsimd.X86.AVX() {
		// This test calls the SIMD kernel directly instead of going through the
		// production dispatcher, so only run it on AVX-capable hosts.
		t.Skip("direct celtExp2x4 oracle requires AVX hardware")
	}
	libopustest.RequireOracle(t)
	if denormTargetV3FMA {
		target, err := libopustooling.ResolveLibopusAMD64Target()
		if err != nil {
			t.Fatalf("resolve libopus AMD64 target: %v", err)
		}
		if target != "v3" {
			t.Fatalf("Go AMD64 v3 exp2 kernel requires a matching libopus v3 oracle, got target %q", target)
		}
	}

	samples := []float32{-60, -51.25, -50.5, -50}
	fracs := [...]float32{0, 0.0625, 0.33325195, 0.5, 0.875, 0.99902344}
	for integer := int32(-50); integer <= 32; integer++ {
		for _, frac := range fracs {
			samples = append(samples, float32(integer)+frac)
		}
	}
	for len(samples)%4 != 0 {
		samples = append(samples, 0)
	}
	want, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeExp2, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	got := make([]float32, len(samples))
	run := func() {
		for i := 0; i < len(samples); i += 4 {
			x := archsimd.LoadFloat32x4Array((*[4]float32)(samples[i : i+4]))
			celtExp2x4(x).StoreArray((*[4]float32)(got[i : i+4]))
		}
	}
	run()
	for i := range samples {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("celtExp2x4(%g)=%08x(%g) want %08x(%g)", samples[i],
				math.Float32bits(got[i]), got[i], math.Float32bits(want[i]), want[i])
		}
	}
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Fatalf("celtExp2x4 allocated: %g allocs/run", allocs)
	}
}
