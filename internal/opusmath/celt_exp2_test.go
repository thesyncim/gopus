package opusmath

import (
	"math"
	"math/rand"
	"testing"
)

// celtExp2FloorReference is CeltExp2 with the integer part taken as
// int32(math.Floor(float64(x))), the literal (int)floor(x) of celt_exp2().
func celtExp2FloorReference(x float32) float32 {
	integer := int32(math.Floor(float64(x)))
	if integer < -50 {
		return 0
	}
	frac := x - float32(integer)
	res := fma32(frac, fma32(frac, fma32(frac, fma32(frac, fma32(frac,
		CeltExp2CoeffA5, CeltExp2CoeffA4), CeltExp2CoeffA3), CeltExp2CoeffA2),
		CeltExp2CoeffA1), CeltExp2CoeffA0)
	bits := math.Float32bits(res)
	bits = uint32(int32(bits)+int32(uint32(integer)<<23)) & 0x7fffffff
	return math.Float32frombits(bits)
}

func TestCeltExp2MatchesFloorReference(t *testing.T) {
	check := func(x float32) {
		t.Helper()
		got, want := CeltExp2(x), celtExp2FloorReference(x)
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("CeltExp2(%g [%08x]) = %08x, want %08x", x, math.Float32bits(x), math.Float32bits(got), math.Float32bits(want))
		}
	}
	for _, x := range []float32{0, float32(math.Copysign(0, -1)), 1, -1, 0.5, -0.5, -50, -50.5, -51, 31.99, 32,
		math.MaxInt32, -math.MaxInt32 - 1, -2147483904, 2147483904, float32(math.Inf(1)), float32(math.Inf(-1)),
		float32(math.NaN()), math.MaxFloat32, -math.MaxFloat32, math.SmallestNonzeroFloat32} {
		check(x)
		check(math.Nextafter32(x, 0))
		check(math.Nextafter32(x, float32(math.Inf(1))))
	}
	rng := rand.New(rand.NewSource(0xe2))
	for range 200000 {
		check(math.Float32frombits(rng.Uint32()))
		check(rng.Float32()*100 - 60)
	}
}
