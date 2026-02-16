//go:build !arm64 && !amd64

package celt

// prefilterPitchXcorr computes lagged correlations with float32 accumulation.
func prefilterPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	_ = x[length-1]
	_ = xcorr[maxPitch-1]
	_ = y[maxPitch+length-2]
	for i := 0; i < maxPitch; i++ {
		sum := float32(0)
		for j := 0; j < length; j++ {
			sum += float32(x[j]) * float32(y[i+j])
		}
		xcorr[i] = float64(sum)
	}
}
