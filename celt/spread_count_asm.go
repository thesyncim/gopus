//go:build amd64 && gopus_spread_asm

package celt

// spreadCountThresholds counts how many coefficients x[0..n-1] satisfy
// x[j]*x[j]*nf < threshold for three thresholds (0.25, 0.0625, 0.015625).
// Returns (t0, t1, t2) counts.
//
//go:noescape
func spreadCountThresholds(x []float64, n int, nf float64) (t0, t1, t2 int)
