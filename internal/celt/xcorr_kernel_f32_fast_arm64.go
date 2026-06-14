//go:build arm64 && !purego

package celt

// xcorrKernel4Float32Fast delegates to xcorrKernel4Float32 on arm64 non-purego
// builds because the encoder pitch-search path reaches xcorrKernel4Float32Fast
// only on non-NEON builds. On arm64 the NEON kernel runs first
// (pitchXcorrUsesNeonFMA=true) so this path is dead — the stub exists only so
// the quality-specific caller compiles without a build-tag guard.
func xcorrKernel4Float32Fast(x, y []float32, sum *[4]float32, length int) {
	xcorrKernel4Float32(x, y, sum, length)
}
