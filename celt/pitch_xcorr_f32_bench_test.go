package celt

import (
	"math/rand"
	"testing"
)

// benchPitchXcorrF32 runs the float32 pitch cross-correlation at the given
// dimensions (the encode prefilter's coarse/fine pitch search shape).
func benchPitchXcorrF32(b *testing.B, length, maxPitch int) {
	rng := rand.New(rand.NewSource(7))
	x := make([]float32, length)
	y := make([]float32, maxPitch+length)
	xcorr := make([]float32, maxPitch)
	for i := range x {
		x[i] = float32(rng.NormFloat64())
	}
	for i := range y {
		y[i] = float32(rng.NormFloat64())
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pitchXCorrFloat32(x, y, xcorr, length, maxPitch)
	}
}

// BenchmarkXcorrF32Coarse mirrors the quarter-resolution coarse pitch search.
func BenchmarkXcorrF32Coarse(b *testing.B) { benchPitchXcorrF32(b, 240, 360) }

// BenchmarkXcorrF32Half mirrors the half-resolution fine pitch search window.
func BenchmarkXcorrF32Half(b *testing.B) { benchPitchXcorrF32(b, 480, 64) }

// BenchmarkXcorrKernel4F32 isolates the inner 4-lag kernel (scalar reference).
func BenchmarkXcorrKernel4F32(b *testing.B) {
	const length = 480
	rng := rand.New(rand.NewSource(7))
	x := make([]float32, length)
	y := make([]float32, length+4)
	for i := range x {
		x[i] = float32(rng.NormFloat64())
	}
	for i := range y {
		y[i] = float32(rng.NormFloat64())
	}
	var sum [4]float32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sum = [4]float32{}
		xcorrKernel4Float32(x, y, &sum, length)
	}
	_ = sum
}
