package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestCeltFIR5MatchesScalar checks the dispatched in-place celt_fir5 against
// the forward scalar loop, bit for bit, over random lengths, taps and inputs,
// including zeros of both signs, which the zero filter memory makes visible.
func TestCeltFIR5MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0xf15))
	val := func() float32 {
		switch rng.Intn(8) {
		case 0:
			return 0
		case 1:
			return float32(math.Copysign(0, -1))
		}
		return float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(9)-4)))
	}
	for iter := range 2000 {
		n := rng.Intn(40)
		if iter%10 == 0 {
			n = 500 + rng.Intn(600)
		}
		var num [5]float32
		for i := range num {
			num[i] = val()
		}
		got := make([]float32, n)
		for i := range got {
			got[i] = val()
		}
		want := append([]float32(nil), got...)
		celtFIR5F32(got, num)
		celtFIR5Scalar(want, num)
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("iter %d n=%d: x[%d]=%v want %v", iter, n, i, got[i], want[i])
			}
		}
	}
}

// TestPitchDownsample2MatchesScalar checks the dispatched factor-2 decimation
// against the scalar loops, bit for bit, for mono and stereo input.
func TestPitchDownsample2MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0xd0a2))
	for iter := range 1000 {
		n := 2 + rng.Intn(40)
		if iter%10 == 0 {
			n = 300 + rng.Intn(400)
		}
		x0 := make([]float32, 2*n)
		x1 := make([]float32, 2*n)
		for i := range x0 {
			x0[i] = float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(9)-4)))
			x1[i] = float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(9)-4)))
		}
		for _, second := range [][]float32{nil, x1} {
			got := make([]float32, n)
			want := make([]float32, n)
			pitchDownsample2(got, x0, second)
			pitchDownsample2Scalar(want, x0, second, 1)
			for i := range want {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("iter %d n=%d stereo=%v: dst[%d]=%v want %v", iter, n, second != nil, i, got[i], want[i])
				}
			}
		}
	}
}

// TestPitchDownsampleSigZeroAllocs locks the pitch_downsample() path, with its
// decimation and celt_fir5, to zero allocations.
func TestPitchDownsampleSigZeroAllocs(t *testing.T) {
	for _, channels := range []int{1, 2} {
		const length = 572
		x := make([]celtSig, channels*2*length)
		for i := range x {
			x[i] = float32((i*37)%101-50) * 0.01
		}
		lp := make([]float32, length)
		pitchDownsampleSig(x, lp, length, channels, 2)
		if allocs := testing.AllocsPerRun(20, func() { pitchDownsampleSig(x, lp, length, channels, 2) }); allocs != 0 {
			t.Fatalf("channels=%d: %v allocs per run, want 0", channels, allocs)
		}
	}
}
