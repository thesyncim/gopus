//go:build !arm64 || purego

package celt

import "math"

// celtInnerProd8FMA32 is the portable fallback for the arm64 NEON kernel. It
// reproduces the 4-lane fused-multiply-add accumulation order of
// celtInnerProdNeonStyle exactly: math.FMA fuses identically to the arm64
// FMADDS/FMLA the asm path emits, and the horizontal reduction order matches.
func celtInnerProd8FMA32(x, y []float32, n int) float32 {
	var acc [4]float32
	i := 0
	for ; i < n-7; i += 8 {
		for lane := 0; lane < 4; lane++ {
			acc[lane] = float32(math.FMA(float64(x[i+lane]), float64(y[i+lane]), float64(acc[lane])))
		}
		for lane := 0; lane < 4; lane++ {
			acc[lane] = float32(math.FMA(float64(x[i+4+lane]), float64(y[i+4+lane]), float64(acc[lane])))
		}
	}
	if n-i >= 4 {
		for lane := 0; lane < 4; lane++ {
			acc[lane] = float32(math.FMA(float64(x[i+lane]), float64(y[i+lane]), float64(acc[lane])))
		}
		i += 4
	}
	sum0 := math.Float32frombits(math.Float32bits(acc[0] + acc[2]))
	sum1 := math.Float32frombits(math.Float32bits(acc[1] + acc[3]))
	sum := math.Float32frombits(math.Float32bits(sum0 + sum1))
	for ; i < n; i++ {
		sum = float32(math.FMA(float64(x[i]), float64(y[i]), float64(sum)))
	}
	return sum
}
