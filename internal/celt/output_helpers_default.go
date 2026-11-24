//go:build !(arm64 && goexperiment.simd && !nosimd && !purego)

package celt

func deemphasisStereoStride1Core(y, x0, x1 []float32, n int, coef, m0, m1 float32, accum bool) (float32, float32) {
	x0 = x0[:n:n]
	x1 = x1[:n:n]
	y = y[: 2*n : 2*n]
	verySmall, scale := deemphasisLoopConsts[0], deemphasisLoopConsts[1]
	j := 0
	if accum {
		for ; j+2 <= n; j += 2 {
			t0 := x0[j] + m0 + verySmall
			u0 := x1[j] + m1 + verySmall
			m0 = mul32(coef, t0)
			m1 = mul32(coef, u0)
			k := j * 2
			y[k] = fma32(scale, t0, y[k])
			y[k+1] = fma32(scale, u0, y[k+1])
			t1 := x0[j+1] + m0 + verySmall
			u1 := x1[j+1] + m1 + verySmall
			m0 = mul32(coef, t1)
			m1 = mul32(coef, u1)
			y[k+2] = fma32(scale, t1, y[k+2])
			y[k+3] = fma32(scale, u1, y[k+3])
		}
		for ; j < n; j++ {
			tmp0 := x0[j] + m0 + verySmall
			tmp1 := x1[j] + m1 + verySmall
			m0 = mul32(coef, tmp0)
			m1 = mul32(coef, tmp1)
			y[2*j] = fma32(scale, tmp0, y[2*j])
			y[2*j+1] = fma32(scale, tmp1, y[2*j+1])
		}
		return m0, m1
	}
	for ; j+2 <= n; j += 2 {
		a0 := x0[j] + verySmall
		b0 := x1[j] + verySmall
		a1 := x0[j+1] + verySmall
		b1 := x1[j+1] + verySmall
		t0 := a0 + m0
		u0 := b0 + m1
		m0 = mul32(coef, t0)
		m1 = mul32(coef, u0)
		k := j * 2
		y[k] = scale * t0
		y[k+1] = scale * u0
		t1 := a1 + m0
		u1 := b1 + m1
		m0 = mul32(coef, t1)
		m1 = mul32(coef, u1)
		y[k+2] = scale * t1
		y[k+3] = scale * u1
	}
	for ; j < n; j++ {
		tmp0 := x0[j] + verySmall + m0
		tmp1 := x1[j] + verySmall + m1
		m0 = mul32(coef, tmp0)
		m1 = mul32(coef, tmp1)
		y[2*j] = scale * tmp0
		y[2*j+1] = scale * tmp1
	}
	return m0, m1
}
