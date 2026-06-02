//go:build !arm64 || purego

package celt

// haar1Stride1NEON is the portable fallback for the arm64 NEON kernel. It runs
// the stride==1 Hadamard butterfly over n0 contiguous (even,odd) pairs with the
// same per-element noFMA32 ops as haar1PairNorm, keeping the purego/amd64 builds
// bit-exact with scalar libopus.
func haar1Stride1NEON(x []float32, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	idx := 0
	for j := 0; j < n0; j++ {
		tmp1 := noFMA32Mul(invSqrt2, x[idx])
		tmp2 := noFMA32Mul(invSqrt2, x[idx+1])
		x[idx] = noFMA32Add(tmp1, tmp2)
		x[idx+1] = noFMA32Sub(tmp1, tmp2)
		idx += 2
	}
}
