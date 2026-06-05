//go:build arm64 && purego

package celt

// prefilterDualInnerProdAsm is the arm64 purego fallback for the NEON dual
// inner-product kernel. It reproduces the 4-lane fused-multiply-add order of
// prefilterDualInnerProdF32NeonOrder exactly, but reaches the FMA through
// fma32 (a*b+c) rather than the portable math.FMA. On arm64 the backend
// contracts a*b+c into one FMADDS, which is bit-identical to
// float32(math.FMA(a,b,c)) for float32 inputs (the f64 round-trip is
// double-rounding-safe) while avoiding its FCVT round-trips. The kernel only
// runs in production on arm64 (libopusFloatInnerProdUsesNeonOrder), where the
// asm path emits the matching vfmaq_f32 accumulation; the libopus-oracle parity
// suite and TestPrefilterDualInnerProdMatchesReference gate the contraction.
func prefilterDualInnerProdAsm(x, y1, y2 []float32, length int) (float32, float32) {
	var acc1 [4]float32
	var acc2 [4]float32
	i := 0
	for ; i < length-7; i += 8 {
		for lane := 0; lane < 4; lane++ {
			acc1[lane] = fma32(x[i+lane], y1[i+lane], acc1[lane])
			acc2[lane] = fma32(x[i+lane], y2[i+lane], acc2[lane])
		}
		for lane := 0; lane < 4; lane++ {
			acc1[lane] = fma32(x[i+4+lane], y1[i+4+lane], acc1[lane])
			acc2[lane] = fma32(x[i+4+lane], y2[i+4+lane], acc2[lane])
		}
	}
	if length-i >= 4 {
		for lane := 0; lane < 4; lane++ {
			acc1[lane] = fma32(x[i+lane], y1[i+lane], acc1[lane])
			acc2[lane] = fma32(x[i+lane], y2[i+lane], acc2[lane])
		}
		i += 4
	}
	xy10 := round32(acc1[0] + acc1[2])
	xy11 := round32(acc1[1] + acc1[3])
	xy20 := round32(acc2[0] + acc2[2])
	xy21 := round32(acc2[1] + acc2[3])
	sum1 := round32(xy10 + xy11)
	sum2 := round32(xy20 + xy21)
	for ; i < length; i++ {
		sum1 = fma32(x[i], y1[i], sum1)
		sum2 = fma32(x[i], y2[i], sum2)
	}
	return sum1, sum2
}
