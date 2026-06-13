package celt

import (
	"math/rand"
	"testing"
)

// benchPitchXcorrF32 runs the float32 pitch cross-correlation at the given
// dimensions (the encode prefilter's coarse/fine pitch search shape).
func benchPitchXcorrF32(b *testing.B, length, maxPitch int) {
	benchPitchXcorrF32With(b, length, maxPitch, pitchXCorrFloat32)
}

func benchPitchXcorrF32With(b *testing.B, length, maxPitch int, fn func([]float32, []float32, []float32, int, int)) {
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
		fn(x, y, xcorr, length, maxPitch)
	}
}

// BenchmarkXcorrF32Coarse mirrors the quarter-resolution coarse pitch search.
func BenchmarkXcorrF32Coarse(b *testing.B) { benchPitchXcorrF32(b, 240, 360) }

// BenchmarkXcorrF32Half mirrors the half-resolution fine pitch search window.
func BenchmarkXcorrF32Half(b *testing.B) { benchPitchXcorrF32(b, 480, 64) }

// BenchmarkXcorrF32TinyCoarse mirrors the 2.5 ms / 8 kHz encoder coarse pitch
// search shape after quarter decimation.
func BenchmarkXcorrF32TinyCoarse(b *testing.B) { benchPitchXcorrF32(b, 5, 244) }

func BenchmarkXcorrF32TinyCoarsePLCOrder(b *testing.B) {
	benchPitchXcorrF32With(b, 5, 244, pitchXCorrFloat32PLC)
}

// BenchmarkXcorrF32TinyFine mirrors the 2.5 ms / 8 kHz encoder fine-search
// window shape.
func BenchmarkXcorrF32TinyFine(b *testing.B) { benchPitchXcorrF32(b, 10, 10) }

func BenchmarkXcorrF32TinyFinePLCOrder(b *testing.B) {
	benchPitchXcorrF32With(b, 10, 10, pitchXCorrFloat32PLC)
}

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
