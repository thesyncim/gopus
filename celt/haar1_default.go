//go:build !arm64 && !amd64

package celt

// haar1Stride1Asm is a pure Go fallback for non-ARM64 platforms.
func haar1Stride1Asm(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 1 + (n0-1)*2
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		tmp1 := invSqrt2 * float32(x[idx])
		tmp2 := invSqrt2 * float32(x[idx+1])
		x[idx] = float64(tmp1 + tmp2)
		x[idx+1] = float64(tmp1 - tmp2)
		idx += 2
	}
}
