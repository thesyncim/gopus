package silk

import (
	"math"
	"math/rand"
	"testing"
)

// refInnerProductF32 is a simple reference implementation for verification.
func refInnerProductF32(a, b []float32, length int) float64 {
	var sum float64
	for i := 0; i < length; i++ {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// refEnergyF32 is a simple reference implementation for verification.
func refEnergyF32(x []float32, length int) float64 {
	var sum float64
	for i := 0; i < length; i++ {
		d := float64(x[i])
		sum += d * d
	}
	return sum
}

func TestInnerProductF32(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 1000; trial++ {
		n := rng.Intn(512) + 1
		a := make([]float32, n)
		b := make([]float32, n)
		for i := range a {
			a[i] = float32(rng.Float64()*2 - 1)
			b[i] = float32(rng.Float64()*2 - 1)
		}
		got := innerProductF32(a, b, n)
		want := refInnerProductF32(a, b, n)
		if math.Abs(got-want) > 1e-6*math.Abs(want)+1e-12 {
			t.Fatalf("trial %d n=%d: got %v want %v diff %v", trial, n, got, want, got-want)
		}
	}
}

func TestInnerProductF32Edge(t *testing.T) {
	if v := innerProductF32(nil, nil, 0); v != 0 {
		t.Fatalf("length=0: got %v", v)
	}
	if v := innerProductF32(nil, nil, -1); v != 0 {
		t.Fatalf("length=-1: got %v", v)
	}
	a := []float32{1, 2, 3}
	b := []float32{4, 5, 6}
	got := innerProductF32(a, b, 3)
	want := 32.0 // 1*4 + 2*5 + 3*6
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInnerProductF32Lengths(t *testing.T) {
	// Test specific lengths that exercise different code paths:
	// 1 (tail only), 2 (tail only), 3 (tail only),
	// 4 (one loop iteration), 5 (one loop + 1 tail),
	// 7 (one loop + 3 tail), 8 (two loops), etc.
	rng := rand.New(rand.NewSource(99))
	for _, n := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 31, 32, 33, 100, 480} {
		a := make([]float32, n)
		b := make([]float32, n)
		for i := range a {
			a[i] = float32(rng.Float64()*2 - 1)
			b[i] = float32(rng.Float64()*2 - 1)
		}
		got := innerProductF32(a, b, n)
		want := refInnerProductF32(a, b, n)
		if math.Abs(got-want) > 1e-6*math.Abs(want)+1e-12 {
			t.Fatalf("n=%d: got %v want %v diff %v", n, got, want, got-want)
		}
	}
}

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

func TestEnergyF32(t *testing.T) {
	rng := rand.New(rand.NewSource(44))
	for trial := 0; trial < 1000; trial++ {
		n := rng.Intn(512) + 1
		x := make([]float32, n)
		for i := range x {
			x[i] = float32(rng.Float64()*2 - 1)
		}
		got := energyF32(x, n)
		want := refEnergyF32(x, n)
		if math.Abs(got-want) > 1e-6*math.Abs(want)+1e-12 {
			t.Fatalf("trial %d n=%d: got %v want %v diff %v", trial, n, got, want, got-want)
		}
	}
}

func TestEnergyF32Edge(t *testing.T) {
	if v := energyF32(nil, 0); v != 0 {
		t.Fatalf("length=0: got %v", v)
	}
	if v := energyF32(nil, -1); v != 0 {
		t.Fatalf("length=-1: got %v", v)
	}
	x := []float32{3, 4}
	got := energyF32(x, 2)
	want := 25.0 // 9 + 16
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestEnergyF32Lengths(t *testing.T) {
	rng := rand.New(rand.NewSource(100))
	for _, n := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 31, 32, 33, 100, 480} {
		x := make([]float32, n)
		for i := range x {
			x[i] = float32(rng.Float64()*2 - 1)
		}
		got := energyF32(x, n)
		want := refEnergyF32(x, n)
		if math.Abs(got-want) > 1e-6*math.Abs(want)+1e-12 {
			t.Fatalf("n=%d: got %v want %v diff %v", n, got, want, got-want)
		}
	}
}

func BenchmarkInnerProductF32(b *testing.B) {
	n := 480
	a := make([]float32, n)
	bb := make([]float32, n)
	for i := range a {
		a[i] = float32(i) * 0.001
		bb[i] = float32(i) * 0.002
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		innerProductF32(a, bb, n)
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

func BenchmarkEnergyF32(b *testing.B) {
	n := 480
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(i) * 0.001
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		energyF32(x, n)
	}
}
