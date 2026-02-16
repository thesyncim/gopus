//go:build !arm64 && !amd64

package celt

// toneLPCCorr computes three float32 correlations for toneLPC.
func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	for i := 0; i < cnt; i++ {
		xi := x[i]
		r00 += xi * xi
		r01 += xi * x[i+delay]
		r02 += xi * x[i+delay2]
	}
	return
}
