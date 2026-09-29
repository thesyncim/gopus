//go:build (!arm64 && !amd64) || nosimd || purego || !goexperiment.simd

package celt

// haar1Stride1 is the scalar stride==1 Hadamard butterfly over n0 contiguous
// (even,odd) pairs, using the target-selected pair math from haar1PairNorm.
func haar1Stride1(x []float32, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	// Caller slices x to 2*n0, so len(buf)>=2 proves both buf[0] and buf[1] are
	// in bounds — no per-pair bounds checks. Two pairs per iteration halves loop
	// overhead and exposes four independent accumulator slots to the pipeline.
	buf := x
	for len(buf) >= 4 {
		sum0, diff0 := haar1PairValues(invSqrt2, buf[0], buf[1])
		sum1, diff1 := haar1PairValues(invSqrt2, buf[2], buf[3])
		buf[0], buf[1] = sum0, diff0
		buf[2], buf[3] = sum1, diff1
		buf = buf[4:]
	}
	if len(buf) >= 2 {
		buf[0], buf[1] = haar1PairValues(invSqrt2, buf[0], buf[1])
	}
}

// haar1Stride2 is the scalar stride==2 butterfly. The two outer passes are fused into a single 4-element stride loop, which is
// cache-friendlier and eliminates the stride-4 counter that blocked BCE.
func haar1Stride2(x []float32, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	// Each group of 4 = one iteration of the original two outer passes.
	// Caller ensures len(x) >= 4*n0 via the slice argument.
	buf := x
	for len(buf) >= 4 {
		sum0, diff0 := haar1PairValues(invSqrt2, buf[0], buf[2])
		sum1, diff1 := haar1PairValues(invSqrt2, buf[1], buf[3])
		buf[0], buf[2] = sum0, diff0
		buf[1], buf[3] = sum1, diff1
		buf = buf[4:]
	}
}

// haar1Stride4 is the scalar stride==4 butterfly. The four outer passes are fused into a single 8-element stride loop.
func haar1Stride4(x []float32, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	// Each group of 8 = one iteration of the original four outer passes.
	// Caller ensures len(x) >= 8*n0 via the slice argument.
	buf := x
	for len(buf) >= 8 {
		sum0, diff0 := haar1PairValues(invSqrt2, buf[0], buf[4])
		sum1, diff1 := haar1PairValues(invSqrt2, buf[1], buf[5])
		sum2, diff2 := haar1PairValues(invSqrt2, buf[2], buf[6])
		sum3, diff3 := haar1PairValues(invSqrt2, buf[3], buf[7])
		buf[0], buf[4] = sum0, diff0
		buf[1], buf[5] = sum1, diff1
		buf[2], buf[6] = sum2, diff2
		buf[3], buf[7] = sum3, diff3
		buf = buf[8:]
	}
}
