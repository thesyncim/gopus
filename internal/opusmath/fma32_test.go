package opusmath

import (
	"math"
	"math/big"
	"math/rand"
	"testing"
)

// fma32Exact rounds the exact a*b+c to float32 through big.Float.
func fma32Exact(a, b, c float32) float32 {
	x := new(big.Float).SetPrec(0).SetMode(big.ToNearestEven)
	x.SetFloat64(float64(a))
	x.Mul(x, new(big.Float).SetFloat64(float64(b)))
	x.Add(x, new(big.Float).SetFloat64(float64(c)))
	f, _ := x.Float32()
	return f
}

func TestFMA32CorrectlyRounded(t *testing.T) {
	a := math.Float32frombits(0x3fcca800)
	b := math.Float32frombits(0x3f979800)
	c := math.Float32frombits(0xa20c2545)
	if got := math.Float32bits(FMA32(a, b, c)); got != 0x3ff26137 {
		t.Fatalf("FMA32 double-rounding case = %08x, want 3ff26137", got)
	}

	rng := rand.New(rand.NewSource(7))
	randF := func() float32 {
		return math.Float32frombits(rng.Uint32()&0x807fffff | uint32(100+rng.Intn(56))<<23)
	}
	for i := range 200000 {
		a, b := randF(), randF()
		// Place c near the product so the sum straddles float32 ties often.
		c := float32(-float64(a)*float64(b)) * (1 + float32(rng.Intn(3)-1)*0x1p-20)
		if i&1 == 1 {
			c = randF()
		}
		got := FMA32(a, b, c)
		want := fma32Exact(a, b, c)
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("FMA32(%08x, %08x, %08x) = %08x, want %08x",
				math.Float32bits(a), math.Float32bits(b), math.Float32bits(c), math.Float32bits(got), math.Float32bits(want))
		}
	}
}
