//go:build !arm64 || nosimd || !goexperiment.simd

package silk

const usePitchXcorrArm64SIMD = false

func celtPitchXcorrFloatImplArm64SIMD(x, y []float32, out []float32, length, maxPitch int) {}
