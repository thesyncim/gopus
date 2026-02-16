//go:build arm64 || amd64

package celt

// toneLPCCorr computes three float32 correlations (r00, r01, r02) for toneLPC.
// cnt = n - 2*delay, delay2 = 2*delay.
//
//go:noescape
func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32)
