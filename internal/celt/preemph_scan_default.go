//go:build !amd64 || !goexperiment.simd || nosimd

package celt

// rawMaxMinScan folds x into celt_maxabs16's running MAX16/MIN16 extrema.
func rawMaxMinScan(x []float32, maxVal, minVal float32) (float32, float32) {
	return rawMaxMinScanScalar(x, maxVal, minVal)
}

// preemphInterleaved applies celt_preemphasis's single-tap filter to
// channels-interleaved pcm and returns the updated per-channel state.
func preemphInterleaved(pcm, out []float32, total, channels int, coef float32, state [2]float32) [2]float32 {
	return preemphInterleavedScalar(pcm, out, total, channels, coef, state)
}
