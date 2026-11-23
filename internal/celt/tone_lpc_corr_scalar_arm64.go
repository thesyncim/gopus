//go:build arm64 && (!goexperiment.simd || nosimd || purego)

package celt

// The pinned scalar arm64 celt_encoder.c:tone_lpc accumulates each sample in
// ascending order with scalar FMADD, including the final odd sample.
func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	if cnt == 0 {
		return
	}
	x0 := x[:cnt:cnt]
	x1 := x[delay : delay+cnt : delay+cnt]
	x2 := x[delay2 : delay2+cnt : delay2+cnt]
	i := 0
	for ; i+1 < cnt; i += 2 {
		a0 := x0[i]
		r00 = fma32(a0, a0, r00)
		r01 = fma32(a0, x1[i], r01)
		r02 = fma32(a0, x2[i], r02)
		a1 := x0[i+1]
		r00 = fma32(a1, a1, r00)
		r01 = fma32(a1, x1[i+1], r01)
		r02 = fma32(a1, x2[i+1], r02)
	}
	if i < cnt {
		xi := x0[i]
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
	x0 := x[:cnt:cnt]
	x1 := x[1 : 1+cnt : 1+cnt]
	x2 := x[2 : 2+cnt : 2+cnt]
	i := 0
	for ; i+1 < cnt; i += 2 {
		a0 := x0[i]
		r00 = fma32(a0, a0, r00)
		r01 = fma32(a0, x1[i], r01)
		r02 = fma32(a0, x2[i], r02)
		a1 := x0[i+1]
		r00 = fma32(a1, a1, r00)
		r01 = fma32(a1, x1[i+1], r01)
		r02 = fma32(a1, x2[i+1], r02)
	}
	if i < cnt {
		xi := x0[i]
		r00 = fma32(xi, xi, r00)
		r01 = fma32(xi, x1[i], r01)
		r02 = fma32(xi, x2[i], r02)
	}
	return
}
