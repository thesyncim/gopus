//go:build arm64 && goexperiment.simd && !nosimd && !purego

package encoder

import "simd/archsimd"

// gemmAccumF32 performs matrix-vector multiplication with preconverted float32
// weights using ARM64 NEON FMLA.4S, matching scalar FMADD bit-for-bit.
func gemmAccumF32(out []float32, weights []float32, rows, cols, colStride int, x []float32) {
	if rows <= 0 || cols <= 0 {
		return
	}
	_ = out[rows-1]
	_ = x[cols-1]
	_ = weights[(cols-1)*colStride+rows-1]
	switch rows {
	case 2:
		o0 := out[0]
		o1 := out[1]
		for j := range cols {
			xj := x[j]
			w := weights[j*colStride:]
			o0 += w[0] * xj
			o1 += w[1] * xj
		}
		out[0] = o0
		out[1] = o1
		return
	case 24:
		o := out[:24:24]
		v0 := archsimd.LoadFloat32x4Array((*[4]float32)(o[0:4]))
		v1 := archsimd.LoadFloat32x4Array((*[4]float32)(o[4:8]))
		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(o[8:12]))
		v3 := archsimd.LoadFloat32x4Array((*[4]float32)(o[12:16]))
		v4 := archsimd.LoadFloat32x4Array((*[4]float32)(o[16:20]))
		v5 := archsimd.LoadFloat32x4Array((*[4]float32)(o[20:24]))
		off := 0
		for j := range cols {
			xj := archsimd.BroadcastFloat32x4(x[j])
			w := weights[off : off+24 : off+24]
			v0 = archsimd.LoadFloat32x4Array((*[4]float32)(w[0:4])).MulAdd(xj, v0)
			v1 = archsimd.LoadFloat32x4Array((*[4]float32)(w[4:8])).MulAdd(xj, v1)
			v2 = archsimd.LoadFloat32x4Array((*[4]float32)(w[8:12])).MulAdd(xj, v2)
			v3 = archsimd.LoadFloat32x4Array((*[4]float32)(w[12:16])).MulAdd(xj, v3)
			v4 = archsimd.LoadFloat32x4Array((*[4]float32)(w[16:20])).MulAdd(xj, v4)
			v5 = archsimd.LoadFloat32x4Array((*[4]float32)(w[20:24])).MulAdd(xj, v5)
			off += colStride
		}
		v0.StoreArray((*[4]float32)(o[0:4]))
		v1.StoreArray((*[4]float32)(o[4:8]))
		v2.StoreArray((*[4]float32)(o[8:12]))
		v3.StoreArray((*[4]float32)(o[12:16]))
		v4.StoreArray((*[4]float32)(o[16:20]))
		v5.StoreArray((*[4]float32)(o[20:24]))
		return
	case 32:
		o := out[:32:32]
		v0 := archsimd.LoadFloat32x4Array((*[4]float32)(o[0:4]))
		v1 := archsimd.LoadFloat32x4Array((*[4]float32)(o[4:8]))
		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(o[8:12]))
		v3 := archsimd.LoadFloat32x4Array((*[4]float32)(o[12:16]))
		v4 := archsimd.LoadFloat32x4Array((*[4]float32)(o[16:20]))
		v5 := archsimd.LoadFloat32x4Array((*[4]float32)(o[20:24]))
		v6 := archsimd.LoadFloat32x4Array((*[4]float32)(o[24:28]))
		v7 := archsimd.LoadFloat32x4Array((*[4]float32)(o[28:32]))
		off := 0
		for j := range cols {
			xj := archsimd.BroadcastFloat32x4(x[j])
			w := weights[off : off+32 : off+32]
			v0 = archsimd.LoadFloat32x4Array((*[4]float32)(w[0:4])).MulAdd(xj, v0)
			v1 = archsimd.LoadFloat32x4Array((*[4]float32)(w[4:8])).MulAdd(xj, v1)
			v2 = archsimd.LoadFloat32x4Array((*[4]float32)(w[8:12])).MulAdd(xj, v2)
			v3 = archsimd.LoadFloat32x4Array((*[4]float32)(w[12:16])).MulAdd(xj, v3)
			v4 = archsimd.LoadFloat32x4Array((*[4]float32)(w[16:20])).MulAdd(xj, v4)
			v5 = archsimd.LoadFloat32x4Array((*[4]float32)(w[20:24])).MulAdd(xj, v5)
			v6 = archsimd.LoadFloat32x4Array((*[4]float32)(w[24:28])).MulAdd(xj, v6)
			v7 = archsimd.LoadFloat32x4Array((*[4]float32)(w[28:32])).MulAdd(xj, v7)
			off += colStride
		}
		v0.StoreArray((*[4]float32)(o[0:4]))
		v1.StoreArray((*[4]float32)(o[4:8]))
		v2.StoreArray((*[4]float32)(o[8:12]))
		v3.StoreArray((*[4]float32)(o[12:16]))
		v4.StoreArray((*[4]float32)(o[16:20]))
		v5.StoreArray((*[4]float32)(o[20:24]))
		v6.StoreArray((*[4]float32)(o[24:28]))
		v7.StoreArray((*[4]float32)(o[28:32]))
		return
	}
	for j := range cols {
		xj := x[j]
		xj4 := archsimd.BroadcastFloat32x4(xj)
		wOff := j * colStride
		w := weights[wOff : wOff+rows : wOff+rows]
		o := out[:rows:rows]
		i := 0
		for ; i+4 <= rows; i += 4 {
			acc := archsimd.LoadFloat32x4Array((*[4]float32)(o[i : i+4]))
			wv := archsimd.LoadFloat32x4Array((*[4]float32)(w[i : i+4]))
			wv.MulAdd(xj4, acc).StoreArray((*[4]float32)(o[i : i+4]))
		}
		for ; i < rows; i++ {
			o[i] += w[i] * xj
		}
	}
}

func gemmAccumF32Rows24Pair(out0, out1 []float32, weights []float32, cols, colStride int, x []float32) {
	if cols <= 0 {
		return
	}
	o0 := out0[:24:24]
	o1 := out1[:24:24]
	_ = x[cols-1]
	_ = weights[(cols-1)*colStride+47]

	a0 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[0:4]))
	a1 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[4:8]))
	a2 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[8:12]))
	a3 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[12:16]))
	a4 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[16:20]))
	a5 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[20:24]))

	b0 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[0:4]))
	b1 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[4:8]))
	b2 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[8:12]))
	b3 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[12:16]))
	b4 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[16:20]))
	b5 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[20:24]))

	off := 0
	for j := range cols {
		xj := archsimd.BroadcastFloat32x4(x[j])
		w := weights[off : off+48 : off+48]
		a0 = archsimd.LoadFloat32x4Array((*[4]float32)(w[0:4])).MulAdd(xj, a0)
		a1 = archsimd.LoadFloat32x4Array((*[4]float32)(w[4:8])).MulAdd(xj, a1)
		a2 = archsimd.LoadFloat32x4Array((*[4]float32)(w[8:12])).MulAdd(xj, a2)
		a3 = archsimd.LoadFloat32x4Array((*[4]float32)(w[12:16])).MulAdd(xj, a3)
		a4 = archsimd.LoadFloat32x4Array((*[4]float32)(w[16:20])).MulAdd(xj, a4)
		a5 = archsimd.LoadFloat32x4Array((*[4]float32)(w[20:24])).MulAdd(xj, a5)
		b0 = archsimd.LoadFloat32x4Array((*[4]float32)(w[24:28])).MulAdd(xj, b0)
		b1 = archsimd.LoadFloat32x4Array((*[4]float32)(w[28:32])).MulAdd(xj, b1)
		b2 = archsimd.LoadFloat32x4Array((*[4]float32)(w[32:36])).MulAdd(xj, b2)
		b3 = archsimd.LoadFloat32x4Array((*[4]float32)(w[36:40])).MulAdd(xj, b3)
		b4 = archsimd.LoadFloat32x4Array((*[4]float32)(w[40:44])).MulAdd(xj, b4)
		b5 = archsimd.LoadFloat32x4Array((*[4]float32)(w[44:48])).MulAdd(xj, b5)
		off += colStride
	}

	a0.StoreArray((*[4]float32)(o0[0:4]))
	a1.StoreArray((*[4]float32)(o0[4:8]))
	a2.StoreArray((*[4]float32)(o0[8:12]))
	a3.StoreArray((*[4]float32)(o0[12:16]))
	a4.StoreArray((*[4]float32)(o0[16:20]))
	a5.StoreArray((*[4]float32)(o0[20:24]))

	b0.StoreArray((*[4]float32)(o1[0:4]))
	b1.StoreArray((*[4]float32)(o1[4:8]))
	b2.StoreArray((*[4]float32)(o1[8:12]))
	b3.StoreArray((*[4]float32)(o1[12:16]))
	b4.StoreArray((*[4]float32)(o1[16:20]))
	b5.StoreArray((*[4]float32)(o1[20:24]))
}

func gemmAccumF32Rows24Triple(out0, out1, out2 []float32, weights []float32, cols, colStride int, x []float32) {
	if cols <= 0 {
		return
	}
	o0 := out0[:24:24]
	o1 := out1[:24:24]
	o2 := out2[:24:24]
	_ = x[cols-1]
	_ = weights[(cols-1)*colStride+71]

	a0 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[0:4]))
	a1 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[4:8]))
	a2 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[8:12]))
	a3 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[12:16]))
	a4 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[16:20]))
	a5 := archsimd.LoadFloat32x4Array((*[4]float32)(o0[20:24]))

	b0 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[0:4]))
	b1 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[4:8]))
	b2 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[8:12]))
	b3 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[12:16]))
	b4 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[16:20]))
	b5 := archsimd.LoadFloat32x4Array((*[4]float32)(o1[20:24]))

	c0 := archsimd.LoadFloat32x4Array((*[4]float32)(o2[0:4]))
	c1 := archsimd.LoadFloat32x4Array((*[4]float32)(o2[4:8]))
	c2 := archsimd.LoadFloat32x4Array((*[4]float32)(o2[8:12]))
	c3 := archsimd.LoadFloat32x4Array((*[4]float32)(o2[12:16]))
	c4 := archsimd.LoadFloat32x4Array((*[4]float32)(o2[16:20]))
	c5 := archsimd.LoadFloat32x4Array((*[4]float32)(o2[20:24]))

	off := 0
	for j := range cols {
		xj := archsimd.BroadcastFloat32x4(x[j])
		w := weights[off : off+72 : off+72]
		a0 = archsimd.LoadFloat32x4Array((*[4]float32)(w[0:4])).MulAdd(xj, a0)
		a1 = archsimd.LoadFloat32x4Array((*[4]float32)(w[4:8])).MulAdd(xj, a1)
		a2 = archsimd.LoadFloat32x4Array((*[4]float32)(w[8:12])).MulAdd(xj, a2)
		a3 = archsimd.LoadFloat32x4Array((*[4]float32)(w[12:16])).MulAdd(xj, a3)
		a4 = archsimd.LoadFloat32x4Array((*[4]float32)(w[16:20])).MulAdd(xj, a4)
		a5 = archsimd.LoadFloat32x4Array((*[4]float32)(w[20:24])).MulAdd(xj, a5)
		b0 = archsimd.LoadFloat32x4Array((*[4]float32)(w[24:28])).MulAdd(xj, b0)
		b1 = archsimd.LoadFloat32x4Array((*[4]float32)(w[28:32])).MulAdd(xj, b1)
		b2 = archsimd.LoadFloat32x4Array((*[4]float32)(w[32:36])).MulAdd(xj, b2)
		b3 = archsimd.LoadFloat32x4Array((*[4]float32)(w[36:40])).MulAdd(xj, b3)
		b4 = archsimd.LoadFloat32x4Array((*[4]float32)(w[40:44])).MulAdd(xj, b4)
		b5 = archsimd.LoadFloat32x4Array((*[4]float32)(w[44:48])).MulAdd(xj, b5)
		c0 = archsimd.LoadFloat32x4Array((*[4]float32)(w[48:52])).MulAdd(xj, c0)
		c1 = archsimd.LoadFloat32x4Array((*[4]float32)(w[52:56])).MulAdd(xj, c1)
		c2 = archsimd.LoadFloat32x4Array((*[4]float32)(w[56:60])).MulAdd(xj, c2)
		c3 = archsimd.LoadFloat32x4Array((*[4]float32)(w[60:64])).MulAdd(xj, c3)
		c4 = archsimd.LoadFloat32x4Array((*[4]float32)(w[64:68])).MulAdd(xj, c4)
		c5 = archsimd.LoadFloat32x4Array((*[4]float32)(w[68:72])).MulAdd(xj, c5)
		off += colStride
	}

	a0.StoreArray((*[4]float32)(o0[0:4]))
	a1.StoreArray((*[4]float32)(o0[4:8]))
	a2.StoreArray((*[4]float32)(o0[8:12]))
	a3.StoreArray((*[4]float32)(o0[12:16]))
	a4.StoreArray((*[4]float32)(o0[16:20]))
	a5.StoreArray((*[4]float32)(o0[20:24]))

	b0.StoreArray((*[4]float32)(o1[0:4]))
	b1.StoreArray((*[4]float32)(o1[4:8]))
	b2.StoreArray((*[4]float32)(o1[8:12]))
	b3.StoreArray((*[4]float32)(o1[12:16]))
	b4.StoreArray((*[4]float32)(o1[16:20]))
	b5.StoreArray((*[4]float32)(o1[20:24]))

	c0.StoreArray((*[4]float32)(o2[0:4]))
	c1.StoreArray((*[4]float32)(o2[4:8]))
	c2.StoreArray((*[4]float32)(o2[8:12]))
	c3.StoreArray((*[4]float32)(o2[12:16]))
	c4.StoreArray((*[4]float32)(o2[16:20]))
	c5.StoreArray((*[4]float32)(o2[20:24]))
}
