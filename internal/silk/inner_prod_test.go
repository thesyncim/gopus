package silk

import (
	"math"
	"math/rand"
	"testing"
)

// refInnerProductF32 is a high-precision sequential reference for verification.
func refInnerProductF32(a, b []float32, length int) float64 {
	var sum float64
	for i := 0; i < length; i++ {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// refEnergyF32 is a high-precision sequential reference for verification.
func refEnergyF32(x []float32, length int) float64 {
	var sum float64
	for i := 0; i < length; i++ {
		d := float64(x[i])
		sum += d * d
	}
	return sum
}

// TestInnerProductFLPRandom checks innerProductFLP — the silk_inner_product_FLP
// port used on every encoder inner-product path — against a sequential float64
// reference. Both accumulate in float64, but libopus sums four products per step
// into one accumulator while the reference adds one product at a time, so they
// agree only to within float64 grouping error. That difference is genuine and
// intended (matching libopus's grouping is the point); exact libopus parity is
// enforced end-to-end by the byte-exact encoder gate.
func TestInnerProductFLPRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(43))
	for trial := 0; trial < 1000; trial++ {
		n := rng.Intn(512) + 1
		a := make([]float32, n)
		b := make([]float32, n)
		for i := range a {
			a[i] = float32(rng.Float64()*2 - 1)
			b[i] = float32(rng.Float64()*2 - 1)
		}
		got := innerProductFLP(a, b, n)
		want := refInnerProductF32(a, b, n)
		if math.Abs(got-want) > 1e-6*math.Abs(want)+1e-12 {
			t.Fatalf("trial %d n=%d: got %v want %v diff %v", trial, n, got, want, got-want)
		}
	}
}

// TestInnerProductFLPLengths exercises the 4-sample loop and the 1..3 tail.
func TestInnerProductFLPLengths(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for _, n := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 31, 32, 33, 100, 480} {
		a := make([]float32, n)
		b := make([]float32, n)
		for i := range a {
			a[i] = float32(rng.Float64()*2 - 1)
			b[i] = float32(rng.Float64()*2 - 1)
		}
		got := innerProductFLP(a, b, n)
		want := refInnerProductF32(a, b, n)
		if math.Abs(got-want) > 1e-6*math.Abs(want)+1e-12 {
			t.Fatalf("n=%d: got %v want %v diff %v", n, got, want, got-want)
		}
	}
}

// TestEnergyF32LibopusRandom checks energyF32Libopus — the silk_energy_FLP port
// used on every encoder energy path — against a sequential float64 reference, to
// within the same float64 grouping error; exact parity is the byte-exact gate.
func TestEnergyF32LibopusRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(44))
	for trial := 0; trial < 1000; trial++ {
		n := rng.Intn(512) + 1
		x := make([]float32, n)
		for i := range x {
			x[i] = float32(rng.Float64()*2 - 1)
		}
		got := energyF32Libopus(x, n)
		want := refEnergyF32(x, n)
		if math.Abs(got-want) > 1e-6*math.Abs(want)+1e-12 {
			t.Fatalf("trial %d n=%d: got %v want %v diff %v", trial, n, got, want, got-want)
		}
	}
}

func TestEnergyF32LibopusEdge(t *testing.T) {
	if v := energyF32Libopus(nil, 0); v != 0 {
		t.Fatalf("length=0: got %v", v)
	}
	x := []float32{3, 4}
	got := energyF32Libopus(x, 2)
	want := 25.0 // 9 + 16
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}

func BenchmarkInnerProductFLP(b *testing.B) {
	n := 480
	a := make([]float32, n)
	bb := make([]float32, n)
	for i := range a {
		a[i] = float32(i) * 0.001
		bb[i] = float32(i) * 0.002
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		innerProductFLP(a, bb, n)
	}
}

func BenchmarkEnergyF32Libopus(b *testing.B) {
	n := 480
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(i) * 0.001
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		energyF32Libopus(x, n)
	}
}
