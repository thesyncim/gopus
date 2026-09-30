//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"math"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// celt_fir5 accepts these short kernel spans, although the public
// pitch_downsample caller needs at least seven outputs for its lag-4 ACF.
// Compare the kernel-only prefix with exact float32 FMA rounding.
func TestCELTV3PitchFIR5ShortHeadsFMA(t *testing.T) {
	num := [5]float32{0.812345, -0.231234, 0.109876, -0.045678, 0.019876}
	input := [6]float32{0.713579, -0.271828, 0.141421, -0.577216, 0.223607, -0.318309}

	for length := 0; length <= len(input); length++ {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			got := append([]float32(nil), input[:length]...)
			want := fusedFIR5Reference(input[:length], num)
			celtFIR5F32(got, num)
			for i := range want {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("x[%d]=%08x, want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}

			if allocs := testing.AllocsPerRun(20, func() { celtFIR5F32(got, num) }); allocs != 0 {
				t.Fatalf("%v allocs per run, want 0", allocs)
			}
		})
	}
}

func fusedFIR5Reference(input []float32, num [5]float32) []float32 {
	want := make([]float32, len(input))
	for i, current := range input {
		sum := current
		for tap, coefficient := range num {
			previous := float32(0)
			if i > tap {
				previous = input[i-tap-1]
			}
			sum = opusmath.FMA32(coefficient, previous, sum)
		}
		want[i] = sum
	}
	return want
}
