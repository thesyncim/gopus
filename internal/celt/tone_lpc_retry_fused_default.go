//go:build !arm64

package celt

func toneLPCRetry48kMonoFast(x []float32, maxDelay int) (float32, float32, bool, int) {
	return toneLPCRetry48kMonoScalarFused(x, maxDelay)
}
