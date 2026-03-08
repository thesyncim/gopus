//go:build amd64 && gopus_spread_asm

package celt

//go:noescape
func spreadCountThresholdsAVX(x []float64, n int, nf float64) (t0, t1, t2 int)

func spreadCountThresholds(x []float64, n int, nf float64) (t0, t1, t2 int) {
	if amd64HasAVX {
		return spreadCountThresholdsAVX(x, n, nf)
	}
	for j := 0; j < n; j++ {
		v := x[j]
		x2N := v * v * nf
		if x2N < 0.25 {
			t0++
		}
		if x2N < 0.0625 {
			t1++
		}
		if x2N < 0.015625 {
			t2++
		}
	}
	return
}
