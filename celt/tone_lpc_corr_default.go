//go:build purego || (!amd64 && !(arm64 && gopus_neon_tone_lpc_corr))

package celt

// toneLPCCorr computes three float32 correlations for toneLPC.
func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	x0 := x[:cnt:cnt]
	x1 := x[delay : delay+cnt : delay+cnt]
	x2 := x[delay2 : delay2+cnt : delay2+cnt]
	for i, xi := range x0 {
		r00 += xi * xi
		r01 += xi * x1[i]
		r02 += xi * x2[i]
	}
	return
}
