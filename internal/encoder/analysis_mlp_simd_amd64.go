//go:build amd64 && goexperiment.simd && !nosimd && !purego

package encoder

import "simd/archsimd"

var analysisMLPGemmUseAVX2 = archsimd.X86.AVX2() && (!analysisMLPGemmUseFMA || archsimd.X86.FMA())

func analysisMLPFMA8(w, x, acc archsimd.Float32x8) archsimd.Float32x8 {
	if analysisMLPGemmUseFMA {
		return w.MulAdd(x, acc)
	}
	return acc.Add(w.Mul(x))
}

func analysisMLPFMA4(w, x, acc archsimd.Float32x4) archsimd.Float32x4 {
	if analysisMLPGemmUseFMA {
		return w.MulAdd(x, acc)
	}
	return acc.Add(w.Mul(x))
}

// gemmAccumF32 performs matrix-vector multiplication with preconverted float32
// weights using 8-wide AMD64 AVX2 vectors, preserving the exact per-lane
// rounding of the matched amd64 scalar lane.
func gemmAccumF32(out []float32, weights []float32, rows, cols, colStride int, x []float32) {
	if rows <= 0 || cols <= 0 {
		return
	}
	_ = out[rows-1]
	_ = x[cols-1]
	_ = weights[(cols-1)*colStride+rows-1]
	if rows == 2 {
		o0 := out[0]
		o1 := out[1]
		for j := range cols {
			xj := x[j]
			w := weights[j*colStride:]
			o0 += analysisMLPGemmProduct(w[0], xj)
			o1 += analysisMLPGemmProduct(w[1], xj)
		}
		out[0] = o0
		out[1] = o1
		return
	}
	if analysisMLPGemmUseAVX2 {
		gemmAccumF32AVX2(out, weights, rows, cols, colStride, x)
		return
	}
	for j := range cols {
		xj := x[j]
		wOff := j * colStride
		w := weights[wOff : wOff+rows : wOff+rows]
		for i := range rows {
			out[i] += analysisMLPGemmProduct(w[i], xj)
		}
	}
}

//go:noinline
func gemmAccumF32AVX2(out []float32, weights []float32, rows, cols, colStride int, x []float32) {
	switch rows {
	case 24:
		o := out[:24:24]
		v0 := archsimd.LoadFloat32x8Array((*[8]float32)(o[0:8]))
		v1 := archsimd.LoadFloat32x8Array((*[8]float32)(o[8:16]))
		v2 := archsimd.LoadFloat32x8Array((*[8]float32)(o[16:24]))
		off := 0
		for j := range cols {
			xj := archsimd.BroadcastFloat32x8(x[j])
			w := weights[off : off+24 : off+24]
			v0 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[0:8])), xj, v0)
			v1 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[8:16])), xj, v1)
			v2 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[16:24])), xj, v2)
			off += colStride
		}
		v0.StoreArray((*[8]float32)(o[0:8]))
		v1.StoreArray((*[8]float32)(o[8:16]))
		v2.StoreArray((*[8]float32)(o[16:24]))
		archsimd.ClearAVXUpperBits()
		return
	case 32:
		o := out[:32:32]
		v0 := archsimd.LoadFloat32x8Array((*[8]float32)(o[0:8]))
		v1 := archsimd.LoadFloat32x8Array((*[8]float32)(o[8:16]))
		v2 := archsimd.LoadFloat32x8Array((*[8]float32)(o[16:24]))
		v3 := archsimd.LoadFloat32x8Array((*[8]float32)(o[24:32]))
		off := 0
		for j := range cols {
			xj := archsimd.BroadcastFloat32x8(x[j])
			w := weights[off : off+32 : off+32]
			v0 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[0:8])), xj, v0)
			v1 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[8:16])), xj, v1)
			v2 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[16:24])), xj, v2)
			v3 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[24:32])), xj, v3)
			off += colStride
		}
		v0.StoreArray((*[8]float32)(o[0:8]))
		v1.StoreArray((*[8]float32)(o[8:16]))
		v2.StoreArray((*[8]float32)(o[16:24]))
		v3.StoreArray((*[8]float32)(o[24:32]))
		archsimd.ClearAVXUpperBits()
		return
	}
	o := out[:rows:rows]
	for j := range cols {
		xj := x[j]
		xj8 := archsimd.BroadcastFloat32x8(xj)
		wOff := j * colStride
		w := weights[wOff : wOff+rows : wOff+rows]
		i := 0
		for ; i+8 <= rows; i += 8 {
			acc := archsimd.LoadFloat32x8Array((*[8]float32)(o[i : i+8]))
			wv := archsimd.LoadFloat32x8Array((*[8]float32)(w[i : i+8]))
			analysisMLPFMA8(wv, xj8, acc).StoreArray((*[8]float32)(o[i : i+8]))
		}
		if i+4 <= rows {
			xj4 := archsimd.BroadcastFloat32x4(xj)
			acc := archsimd.LoadFloat32x4Array((*[4]float32)(o[i : i+4]))
			wv := archsimd.LoadFloat32x4Array((*[4]float32)(w[i : i+4]))
			analysisMLPFMA4(wv, xj4, acc).StoreArray((*[4]float32)(o[i : i+4]))
			i += 4
		}
		for ; i < rows; i++ {
			o[i] += analysisMLPGemmProduct(w[i], xj)
		}
	}
	archsimd.ClearAVXUpperBits()
}

func gemmAccumF32Rows24Pair(out0, out1 []float32, weights []float32, cols, colStride int, x []float32) {
	if cols <= 0 {
		return
	}
	_ = out0[23]
	_ = out1[23]
	_ = x[cols-1]
	_ = weights[(cols-1)*colStride+47]
	if analysisMLPGemmUseAVX2 {
		gemmAccumF32Rows24PairAVX2(out0, out1, weights, cols, colStride, x)
		return
	}
	gemmAccumF32(out0, weights, 24, cols, colStride, x)
	gemmAccumF32(out1, weights[24:], 24, cols, colStride, x)
}

//go:noinline
func gemmAccumF32Rows24PairAVX2(out0, out1 []float32, weights []float32, cols, colStride int, x []float32) {
	o0 := out0[:24:24]
	o1 := out1[:24:24]

	a0 := archsimd.LoadFloat32x8Array((*[8]float32)(o0[0:8]))
	a1 := archsimd.LoadFloat32x8Array((*[8]float32)(o0[8:16]))
	a2 := archsimd.LoadFloat32x8Array((*[8]float32)(o0[16:24]))

	b0 := archsimd.LoadFloat32x8Array((*[8]float32)(o1[0:8]))
	b1 := archsimd.LoadFloat32x8Array((*[8]float32)(o1[8:16]))
	b2 := archsimd.LoadFloat32x8Array((*[8]float32)(o1[16:24]))

	off := 0
	for j := range cols {
		xj := archsimd.BroadcastFloat32x8(x[j])
		w := weights[off : off+48 : off+48]
		a0 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[0:8])), xj, a0)
		a1 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[8:16])), xj, a1)
		a2 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[16:24])), xj, a2)
		b0 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[24:32])), xj, b0)
		b1 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[32:40])), xj, b1)
		b2 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[40:48])), xj, b2)
		off += colStride
	}

	a0.StoreArray((*[8]float32)(o0[0:8]))
	a1.StoreArray((*[8]float32)(o0[8:16]))
	a2.StoreArray((*[8]float32)(o0[16:24]))

	b0.StoreArray((*[8]float32)(o1[0:8]))
	b1.StoreArray((*[8]float32)(o1[8:16]))
	b2.StoreArray((*[8]float32)(o1[16:24]))
	archsimd.ClearAVXUpperBits()
}

func gemmAccumF32Rows24Triple(out0, out1, out2 []float32, weights []float32, cols, colStride int, x []float32) {
	if cols <= 0 {
		return
	}
	_ = out0[23]
	_ = out1[23]
	_ = out2[23]
	_ = x[cols-1]
	_ = weights[(cols-1)*colStride+71]
	if analysisMLPGemmUseAVX2 {
		gemmAccumF32Rows24TripleAVX2(out0, out1, out2, weights, cols, colStride, x)
		return
	}
	gemmAccumF32(out0, weights, 24, cols, colStride, x)
	gemmAccumF32(out1, weights[24:], 24, cols, colStride, x)
	gemmAccumF32(out2, weights[48:], 24, cols, colStride, x)
}

//go:noinline
func gemmAccumF32Rows24TripleAVX2(out0, out1, out2 []float32, weights []float32, cols, colStride int, x []float32) {
	o0 := out0[:24:24]
	o1 := out1[:24:24]
	o2 := out2[:24:24]

	a0 := archsimd.LoadFloat32x8Array((*[8]float32)(o0[0:8]))
	a1 := archsimd.LoadFloat32x8Array((*[8]float32)(o0[8:16]))
	a2 := archsimd.LoadFloat32x8Array((*[8]float32)(o0[16:24]))

	b0 := archsimd.LoadFloat32x8Array((*[8]float32)(o1[0:8]))
	b1 := archsimd.LoadFloat32x8Array((*[8]float32)(o1[8:16]))
	b2 := archsimd.LoadFloat32x8Array((*[8]float32)(o1[16:24]))

	c0 := archsimd.LoadFloat32x8Array((*[8]float32)(o2[0:8]))
	c1 := archsimd.LoadFloat32x8Array((*[8]float32)(o2[8:16]))
	c2 := archsimd.LoadFloat32x8Array((*[8]float32)(o2[16:24]))

	off := 0
	for j := range cols {
		xj := archsimd.BroadcastFloat32x8(x[j])
		w := weights[off : off+72 : off+72]
		a0 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[0:8])), xj, a0)
		a1 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[8:16])), xj, a1)
		a2 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[16:24])), xj, a2)
		b0 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[24:32])), xj, b0)
		b1 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[32:40])), xj, b1)
		b2 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[40:48])), xj, b2)
		c0 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[48:56])), xj, c0)
		c1 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[56:64])), xj, c1)
		c2 = analysisMLPFMA8(archsimd.LoadFloat32x8Array((*[8]float32)(w[64:72])), xj, c2)
		off += colStride
	}

	a0.StoreArray((*[8]float32)(o0[0:8]))
	a1.StoreArray((*[8]float32)(o0[8:16]))
	a2.StoreArray((*[8]float32)(o0[16:24]))

	b0.StoreArray((*[8]float32)(o1[0:8]))
	b1.StoreArray((*[8]float32)(o1[8:16]))
	b2.StoreArray((*[8]float32)(o1[16:24]))

	c0.StoreArray((*[8]float32)(o2[0:8]))
	c1.StoreArray((*[8]float32)(o2[8:16]))
	c2.StoreArray((*[8]float32)(o2[16:24]))
	archsimd.ClearAVXUpperBits()
}
