package celt

import (
	"math"
	"math/rand"
	"testing"
)

func refInnerProd(x, y []float64, length int) float64 {
	sum := 0.0
	for i := 0; i < length; i++ {
		sum += x[i] * y[i]
	}
	return sum
}

func refDualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	sum1, sum2 := 0.0, 0.0
	for i := 0; i < length; i++ {
		sum1 += x[i] * y1[i]
		sum2 += x[i] * y2[i]
	}
	return sum1, sum2
}

func refPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	for i := 0; i < maxPitch; i++ {
		sum := 0.0
		for j := 0; j < length; j++ {
			sum += x[j] * y[i+j]
		}
		xcorr[i] = sum
	}
}

func TestCeltInnerProd(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 1000; trial++ {
		n := rng.Intn(512) + 1
		x := make([]float64, n)
		y := make([]float64, n)
		for i := range x {
			x[i] = rng.Float64()*2 - 1
			y[i] = rng.Float64()*2 - 1
		}
		got := celtInnerProd(x, y, n)
		want := refInnerProd(x, y, n)
		if math.Abs(got-want) > 1e-6*math.Abs(want)+1e-12 {
			t.Fatalf("trial %d n=%d: got %v want %v", trial, n, got, want)
		}
	}
}

func TestCeltInnerProdEdge(t *testing.T) {
	if v := celtInnerProd(nil, nil, 0); v != 0 {
		t.Fatalf("length=0: got %v", v)
	}
	if v := celtInnerProd(nil, nil, -1); v != 0 {
		t.Fatalf("length=-1: got %v", v)
	}
	x := []float64{1, 2, 3}
	y := []float64{4, 5, 6}
	got := celtInnerProd(x, y, 3)
	want := 32.0 // 1*4 + 2*5 + 3*6
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDualInnerProd(t *testing.T) {
	rng := rand.New(rand.NewSource(43))
	for trial := 0; trial < 1000; trial++ {
		n := rng.Intn(512) + 1
		x := make([]float64, n)
		y1 := make([]float64, n)
		y2 := make([]float64, n)
		for i := range x {
			x[i] = rng.Float64()*2 - 1
			y1[i] = rng.Float64()*2 - 1
			y2[i] = rng.Float64()*2 - 1
		}
		g1, g2 := dualInnerProd(x, y1, y2, n)
		w1, w2 := refDualInnerProd(x, y1, y2, n)
		if math.Abs(g1-w1) > 1e-6*math.Abs(w1)+1e-12 {
			t.Fatalf("trial %d n=%d sum1: got %v want %v", trial, n, g1, w1)
		}
		if math.Abs(g2-w2) > 1e-6*math.Abs(w2)+1e-12 {
			t.Fatalf("trial %d n=%d sum2: got %v want %v", trial, n, g2, w2)
		}
	}
}

func TestCeltPitchXcorr(t *testing.T) {
	rng := rand.New(rand.NewSource(44))
	for trial := 0; trial < 200; trial++ {
		length := rng.Intn(256) + 1
		maxPitch := rng.Intn(64) + 1
		x := make([]float64, length)
		y := make([]float64, maxPitch+length)
		for i := range x {
			x[i] = rng.Float64()*2 - 1
		}
		for i := range y {
			y[i] = rng.Float64()*2 - 1
		}
		got := make([]float64, maxPitch)
		want := make([]float64, maxPitch)
		celtPitchXcorr(x, y, got, length, maxPitch)
		refPitchXcorr(x, y, want, length, maxPitch)
		for i := 0; i < maxPitch; i++ {
			if math.Abs(got[i]-want[i]) > 1e-6*math.Abs(want[i])+1e-12 {
				t.Fatalf("trial %d i=%d: got %v want %v", trial, i, got[i], want[i])
			}
		}
	}
}

func TestCeltPitchXcorrEdge(t *testing.T) {
	// maxPitch < 4 (no 4-way unrolling)
	x := []float64{1, 1, 1}
	y := []float64{1, 2, 3, 4, 5}
	xcorr := make([]float64, 3)
	celtPitchXcorr(x, y, xcorr, 3, 3)
	// xcorr[0] = 1*1 + 1*2 + 1*3 = 6
	// xcorr[1] = 1*2 + 1*3 + 1*4 = 9
	// xcorr[2] = 1*3 + 1*4 + 1*5 = 12
	want := []float64{6, 9, 12}
	for i := range want {
		if xcorr[i] != want[i] {
			t.Fatalf("i=%d: got %v want %v", i, xcorr[i], want[i])
		}
	}

	// maxPitch = 0
	celtPitchXcorr(nil, nil, nil, 0, 0)
	// length = 0
	celtPitchXcorr(nil, nil, nil, 0, 5)
}

func BenchmarkCeltInnerProd(b *testing.B) {
	n := 480
	x := make([]float64, n)
	y := make([]float64, n)
	for i := range x {
		x[i] = float64(i) * 0.001
		y[i] = float64(i) * 0.002
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		celtInnerProd(x, y, n)
	}
}

func BenchmarkDualInnerProd(b *testing.B) {
	n := 480
	x := make([]float64, n)
	y1 := make([]float64, n)
	y2 := make([]float64, n)
	for i := range x {
		x[i] = float64(i) * 0.001
		y1[i] = float64(i) * 0.002
		y2[i] = float64(i) * 0.003
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dualInnerProd(x, y1, y2, n)
	}
}

func BenchmarkCeltPitchXcorr(b *testing.B) {
	length := 480
	maxPitch := 64
	x := make([]float64, length)
	y := make([]float64, maxPitch+length)
	xcorr := make([]float64, maxPitch)
	for i := range x {
		x[i] = float64(i) * 0.001
	}
	for i := range y {
		y[i] = float64(i) * 0.002
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		celtPitchXcorr(x, y, xcorr, length, maxPitch)
	}
}
