//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// deemphasisStereoStride1Core runs the single-tap stereo deemphasis recurrence
// for contiguous x0/x1 with both channels paired in the low two lanes of a
// Float32x4 vector, processing four stereo frames per iteration and matching
// deemphasisStereoStride1Scalar bit-for-bit.
func deemphasisStereoStride1Core(y, x0, x1 []float32, n int, coef, m0, m1 float32, accum bool) (float32, float32) {
	x0 = x0[:n:n]
	x1 = x1[:n:n]
	y = y[: 2*n : 2*n]
	verySmall, scale := deemphasisLoopConsts[0], deemphasisLoopConsts[1]
	vs4 := archsimd.BroadcastFloat32x4(verySmall)
	coef4 := archsimd.BroadcastFloat32x4(coef)
	scale4 := archsimd.BroadcastFloat32x4(scale)
	m := archsimd.LoadFloat32x4Array(&[4]float32{m0, m1, 0, 0})

	j := 0
	if accum {
		for ; j+4 <= n; j += 4 {
			a := archsimd.LoadFloat32x4Array((*[4]float32)(x0[j : j+4])).ToBits()
			b := archsimd.LoadFloat32x4Array((*[4]float32)(x1[j : j+4])).ToBits()
			p01 := a.InterleaveLo(b)
			p23 := a.InterleaveHi(b)
			p01q := p01.ReshapeToUint64s()
			p23q := p23.ReshapeToUint64s()
			p0 := p01.BitsToFloat32()
			p1 := p01q.InterleaveHi(p01q).ReshapeToUint32s().BitsToFloat32()
			p2 := p23.BitsToFloat32()
			p3 := p23q.InterleaveHi(p23q).ReshapeToUint32s().BitsToFloat32()

			// accum branch preserves (x + m) + verySmall addition order.
			t0 := p0.Add(m).Add(vs4)
			m = t0.Mul(coef4)
			t1 := p1.Add(m).Add(vs4)
			m = t1.Mul(coef4)
			k := j * 2
			t01 := t0.ToBits().ReshapeToUint64s().InterleaveLo(t1.ToBits().ReshapeToUint64s()).ReshapeToUint32s().BitsToFloat32()
			y01 := archsimd.LoadFloat32x4Array((*[4]float32)(y[k : k+4]))
			scale4.MulAdd(t01, y01).StoreArray((*[4]float32)(y[k : k+4]))

			t2 := p2.Add(m).Add(vs4)
			m = t2.Mul(coef4)
			t3 := p3.Add(m).Add(vs4)
			m = t3.Mul(coef4)
			t23 := t2.ToBits().ReshapeToUint64s().InterleaveLo(t3.ToBits().ReshapeToUint64s()).ReshapeToUint32s().BitsToFloat32()
			y23 := archsimd.LoadFloat32x4Array((*[4]float32)(y[k+4 : k+8]))
			scale4.MulAdd(t23, y23).StoreArray((*[4]float32)(y[k+4 : k+8]))
		}
		m0, m1 = m.GetElem(0), m.GetElem(1)
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

	for ; j+4 <= n; j += 4 {
		a := archsimd.LoadFloat32x4Array((*[4]float32)(x0[j : j+4])).Add(vs4).ToBits()
		b := archsimd.LoadFloat32x4Array((*[4]float32)(x1[j : j+4])).Add(vs4).ToBits()
		p01 := a.InterleaveLo(b)
		p23 := a.InterleaveHi(b)
		p01q := p01.ReshapeToUint64s()
		p23q := p23.ReshapeToUint64s()
		p0 := p01.BitsToFloat32()
		p1 := p01q.InterleaveHi(p01q).ReshapeToUint32s().BitsToFloat32()
		p2 := p23.BitsToFloat32()
		p3 := p23q.InterleaveHi(p23q).ReshapeToUint32s().BitsToFloat32()

		t0 := p0.Add(m)
		m = t0.Mul(coef4)
		t1 := p1.Add(m)
		m = t1.Mul(coef4)
		k := j * 2
		t01 := t0.ToBits().ReshapeToUint64s().InterleaveLo(t1.ToBits().ReshapeToUint64s()).ReshapeToUint32s().BitsToFloat32()
		t01.Mul(scale4).StoreArray((*[4]float32)(y[k : k+4]))

		t2 := p2.Add(m)
		m = t2.Mul(coef4)
		t3 := p3.Add(m)
		m = t3.Mul(coef4)
		t23 := t2.ToBits().ReshapeToUint64s().InterleaveLo(t3.ToBits().ReshapeToUint64s()).ReshapeToUint32s().BitsToFloat32()
		t23.Mul(scale4).StoreArray((*[4]float32)(y[k+4 : k+8]))
	}
	m0, m1 = m.GetElem(0), m.GetElem(1)
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
