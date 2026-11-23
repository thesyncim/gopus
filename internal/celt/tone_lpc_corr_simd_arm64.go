//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

// The pinned SIMD arm64 celt_encoder.c:tone_lpc rounds each product before an
// ordered add on the even prefix and uses FMA on the final odd sample. Keeping
// the three accumulators in scalar FP registers with explicit float32(...)
// products preserves exact ascending rounding while avoiding per-sample vector
// lane inserts.
func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	if cnt == 0 {
		return
	}
	_ = x[delay2+cnt-1]
	x1 := x[delay:]
	x2 := x[delay2:]
	i := 0
	for ; i+1 < cnt; i += 2 {
		a0 := x[i]
		r00 += float32(a0 * a0)
		r01 += float32(a0 * x1[i])
		r02 += float32(a0 * x2[i])
		a1 := x[i+1]
		r00 += float32(a1 * a1)
		r01 += float32(a1 * x1[i+1])
		r02 += float32(a1 * x2[i+1])
	}
	if i < cnt {
		xi := x[i]
		r00 = fma32(xi, xi, r00)
		r01 = fma32(xi, x1[i], r01)
		r02 = fma32(xi, x2[i], r02)
	}
	return
}

func toneLPCCorrDelay1(x []float32, cnt int) (r00, r01, r02 float32) {
	if cnt == 0 {
		return
	}
	_ = x[cnt+1]
	a0 := x[0]
	a1 := x[1]
	i := 0
	for ; i+1 < cnt; i += 2 {
		a2 := x[i+2]
		a3 := x[i+3]
		r00 += float32(a0 * a0)
		r01 += float32(a0 * a1)
		r02 += float32(a0 * a2)
		r00 += float32(a1 * a1)
		r01 += float32(a1 * a2)
		r02 += float32(a1 * a3)
		a0, a1 = a2, a3
	}
	if i < cnt {
		a2 := x[i+2]
		r00 = fma32(a0, a0, r00)
		r01 = fma32(a0, a1, r01)
		r02 = fma32(a0, a2, r02)
	}
	return
}
