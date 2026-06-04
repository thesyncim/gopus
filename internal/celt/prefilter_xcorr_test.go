package celt

import (
	"math"
	"math/rand"
	"testing"
)

// goPrefilterPitchXcorr is the reference Go implementation.
func goPrefilterPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	for i := 0; i < maxPitch; i++ {
		sum := float32(0)
		for j := 0; j < length; j++ {
			sum += float32(x[j]) * float32(y[i+j])
		}
		xcorr[i] = float64(sum)
	}
}

// f32Close checks if two float64 values (representing widened float32 results)
// are close enough. SIMD accumulation reorders float32 additions, producing
// differences bounded by n*eps(float32) where n is the vector length.
func f32Close(got, want float64) bool {
	diff := math.Abs(got - want)
	if diff <= 1e-5 {
		return true
	}
	denom := math.Abs(want)
	if denom < 1e-10 {
		return diff < 1e-5
	}
	return diff/denom < 1e-5
}

func TestPrefilterPitchXcorr(t *testing.T) {
	rng := rand.New(rand.NewSource(44))
	for trial := 0; trial < 200; trial++ {
		length := rng.Intn(300) + 1
		maxPitch := rng.Intn(100) + 1
		x := make([]float64, length)
		y := make([]float64, length+maxPitch)
		for i := range x {
			x[i] = rng.Float64()*2 - 1
		}
		for i := range y {
			y[i] = rng.Float64()*2 - 1
		}
		got := make([]float64, maxPitch)
		want := make([]float64, maxPitch)
		prefilterPitchXcorr(x, y, got, length, maxPitch)
		goPrefilterPitchXcorr(x, y, want, length, maxPitch)
		for i := 0; i < maxPitch; i++ {
			if !f32Close(got[i], want[i]) {
				t.Fatalf("trial %d (length=%d, maxPitch=%d): xcorr[%d] got %v, want %v (diff=%e)",
					trial, length, maxPitch, i, got[i], want[i], math.Abs(got[i]-want[i]))
			}
		}
	}
}

func TestPrefilterPitchXcorrEdge(t *testing.T) {
	// maxPitch < 4 (no 4-way outer loop)
	x := []float64{1, 1, 1, 1, 1}
	y := []float64{1, 2, 3, 4, 5, 6, 7}
	got := make([]float64, 3)
	want := make([]float64, 3)
	prefilterPitchXcorr(x, y, got, 5, 3)
	goPrefilterPitchXcorr(x, y, want, 5, 3)
	for i := 0; i < 3; i++ {
		if got[i] != want[i] {
			t.Fatalf("xcorr[%d]: got %v, want %v", i, got[i], want[i])
		}
	}
}

func BenchmarkPrefilterPitchXcorr(b *testing.B) {
	rng := rand.New(rand.NewSource(99))
	length := 240
	maxPitch := 256
	x := make([]float64, length)
	y := make([]float64, length+maxPitch)
	xcorr := make([]float64, maxPitch)
	for i := range x {
		x[i] = rng.Float64()*2 - 1
	}
	for i := range y {
		y[i] = rng.Float64()*2 - 1
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prefilterPitchXcorr(x, y, xcorr, length, maxPitch)
	}
}
