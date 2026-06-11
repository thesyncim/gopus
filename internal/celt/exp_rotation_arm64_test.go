//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestExpRotation1StrideNeonBitExact pins the vectorized spreading-rotation
// pass to the scalar expRotation1Norm loops bit-for-bit across the stride
// geometries the spreading rotation produces (and off-grid lengths that
// exercise both scalar tails).
func TestExpRotation1StrideNeonBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	type shape struct{ length, stride int }
	shapes := []shape{
		{176, 12}, {88, 8}, {44, 6}, {36, 5}, {24, 4},
		{21, 4}, {23, 5}, {47, 7}, {9, 4}, {8, 4}, {175, 13},
	}
	for _, s := range shapes {
		for trial := 0; trial < 8; trial++ {
			c := opusVal16(rng.Float32()*2 - 1)
			sn := opusVal16(rng.Float32()*2 - 1)
			x := make([]celtNorm, s.length)
			for i := range x {
				x[i] = celtNorm(rng.NormFloat64())
			}
			got := append([]celtNorm(nil), x...)
			want := append([]celtNorm(nil), x...)
			expRotation1StrideNeon(got, s.length, s.stride, c, sn)
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
