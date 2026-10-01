package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestExpRotation1StrideSIMDBitExact pins the vectorized spreading-rotation
// pass to the scalar expRotation1Norm loops bit-for-bit across the stride
// geometries the spreading rotation produces (and off-grid lengths that
// exercise both scalar tails).
func TestExpRotation1StrideSIMDBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	type shape struct{ length, stride int }
	shapes := []shape{
		{176, 12}, {88, 8}, {44, 6}, {36, 5}, {24, 4},
		{21, 4}, {23, 5}, {47, 7}, {9, 4}, {8, 4}, {175, 13}, {5, 4}, {12, 4}, {13, 6},
	}
	for _, s := range shapes {
		for trial := range 8 {
			c := opusVal16(rng.Float32()*2 - 1)
			sn := opusVal16(rng.Float32()*2 - 1)
			x := make([]celtNorm, s.length)
			for i := range x {
				x[i] = celtNorm(rng.NormFloat64())
			}
			got := append([]celtNorm(nil), x...)
			want := append([]celtNorm(nil), x...)
			expRotation1StrideSIMD(got, s.length, s.stride, c, sn)
			expRotation1NormScalar(want[:s.length:s.length], s.length, s.stride, c, sn)
			for k := range want {
				if math.Float32bits(float32(got[k])) != math.Float32bits(float32(want[k])) {
					t.Fatalf("%+v trial %d: x[%d] = %08x, want %08x", s, trial, k,
						math.Float32bits(float32(got[k])), math.Float32bits(float32(want[k])))
				}
			}
		}
	}
}

// expRotation1Reference is exp_rotation1's two strided loops as written in
// celt/vq.c, reading and writing every element through x.
func expRotation1Reference(x []celtNorm, length, stride int, c, s float32) {
	ms := -s
	for i := 0; i < length-stride; i++ {
		x1, x2 := float32(x[i]), float32(x[i+stride])
		x[i+stride] = celtNorm(expRotationMac32(c, x2, s, x1))
		x[i] = celtNorm(expRotationMac32(c, x1, ms, x2))
	}
	for i := length - 2*stride - 1; i >= 0; i-- {
		x1, x2 := float32(x[i]), float32(x[i+stride])
		x[i+stride] = celtNorm(expRotationMac32(c, x2, s, x1))
		x[i] = celtNorm(expRotationMac32(c, x1, ms, x2))
	}
}

// TestExpRotation1MatchesReference pins expRotation1Norm, including the
// register-carried stride-1 loops, to the strided reference bit for bit.
func TestExpRotation1MatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xe4))
	for trial := range 2000 {
		length := 1 + rng.Intn(180)
		stride := 1 + rng.Intn(max(length/2, 1))
		if trial%2 == 0 {
			stride = 1
		}
		c := float32(rng.Float64()*2 - 1)
		sn := float32(rng.Float64()*2 - 1)
		x := make([]celtNorm, length)
		for i := range x {
			x[i] = celtNorm(rng.NormFloat64())
		}
		got := append([]celtNorm(nil), x...)
		want := append([]celtNorm(nil), x...)
		expRotation1Norm(got, length, stride, opusVal16(c), opusVal16(sn))
		if stride < length {
			expRotation1Reference(want, length, stride, c, sn)
		}
		for k := range want {
			if math.Float32bits(float32(got[k])) != math.Float32bits(float32(want[k])) {
				t.Fatalf("trial %d length %d stride %d: x[%d] = %v, want %v", trial, length, stride, k, got[k], want[k])
			}
		}
	}
}
