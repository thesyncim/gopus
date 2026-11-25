//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"math"
	"strconv"
	"testing"
)

func TestPitchAutocorrFMA32MatchesLibopus(t *testing.T) {
	requireCELTV3OracleTarget(t)
	requirePairedCELTOracleMode(t)
	libopustest.RequireOracle(t)

	const length = 8
	pcm := makeCELTPLCTestSignal(length*2, 0x5f150000+length*4+1, 2600)
	x := make([]float32, length)
	x[0] = float32(0.25)*float32(pcm[1]) + float32(0.5)*float32(pcm[0])
	pitchDownsample2(x, pcm, nil)
	want := probeLibopusRawAutocorr(t, x, 4, nil)
	var got [5]float32
	run := func() {
		pitchAutocorr5F32(x, length, &got)
	}
	run()
	assertFloat32Bits(t, "raw autocorrelation", got[:], want)

	// Warm the target-selected kernel before measuring its steady-state path.
	run()
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Fatalf("raw autocorrelation allocated %g objects per call", allocs)
	}
}

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
