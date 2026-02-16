package silk

import (
	"math/rand"
	"testing"
)

func BenchmarkCeltPitchXcorrFloat(b *testing.B) {
	// Representative sizes for SILK pitch analysis
	// length ~ 80-160 samples (5-10ms at 16kHz)
	// maxPitch ~ 300 samples (to cover wide range)
	length := 120
	maxPitch := 300
	
	// Ensure x is long enough for the kernel window
	x := make([]float32, length)
	y := make([]float32, maxPitch + length + 4)
	out := make([]float32, maxPitch)
	
	rng := rand.New(rand.NewSource(42))
	for i := range x {
		x[i] = rng.Float32()*2 - 1
	}
	for i := range y {
		y[i] = rng.Float32()*2 - 1
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		celtPitchXcorrFloat(x, y, out, length, maxPitch)
	}
}

func BenchmarkXcorrKernelFloat(b *testing.B) {
	// Kernel benchmarks for the 4-way loop
	length := 120
	x := make([]float32, length)
	y := make([]float32, length + 4) // Kernel needs extra padding
	var sum [4]float32
	
	rng := rand.New(rand.NewSource(43))
	for i := range x {
		x[i] = rng.Float32()*2 - 1
	}
	for i := range y {
		y[i] = rng.Float32()*2 - 1
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		xcorrKernelFloat(x, y, &sum, length)
	}
}
