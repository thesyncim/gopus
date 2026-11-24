//go:build !(arm64 && goexperiment.simd && !nosimd && !purego)

package encoder

// gemmAccumF32 performs matrix-vector multiplication with preconverted weights.
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
			o0 += analysisMLPGemmProduct(w[0], xj)
			o1 += analysisMLPGemmProduct(w[1], xj)
		}
		out[0] = o0
		out[1] = o1
		return
	case 24:
		o0, o1, o2, o3 := out[0], out[1], out[2], out[3]
		o4, o5, o6, o7 := out[4], out[5], out[6], out[7]
		o8, o9, o10, o11 := out[8], out[9], out[10], out[11]
		o12, o13, o14, o15 := out[12], out[13], out[14], out[15]
		o16, o17, o18, o19 := out[16], out[17], out[18], out[19]
		o20, o21, o22, o23 := out[20], out[21], out[22], out[23]
		for j := range cols {
			xj := x[j]
			w := weights[j*colStride:]
			o0 += analysisMLPGemmProduct(w[0], xj)
			o1 += analysisMLPGemmProduct(w[1], xj)
			o2 += analysisMLPGemmProduct(w[2], xj)
			o3 += analysisMLPGemmProduct(w[3], xj)
			o4 += analysisMLPGemmProduct(w[4], xj)
			o5 += analysisMLPGemmProduct(w[5], xj)
			o6 += analysisMLPGemmProduct(w[6], xj)
			o7 += analysisMLPGemmProduct(w[7], xj)
			o8 += analysisMLPGemmProduct(w[8], xj)
			o9 += analysisMLPGemmProduct(w[9], xj)
			o10 += analysisMLPGemmProduct(w[10], xj)
			o11 += analysisMLPGemmProduct(w[11], xj)
			o12 += analysisMLPGemmProduct(w[12], xj)
			o13 += analysisMLPGemmProduct(w[13], xj)
			o14 += analysisMLPGemmProduct(w[14], xj)
			o15 += analysisMLPGemmProduct(w[15], xj)
			o16 += analysisMLPGemmProduct(w[16], xj)
			o17 += analysisMLPGemmProduct(w[17], xj)
			o18 += analysisMLPGemmProduct(w[18], xj)
			o19 += analysisMLPGemmProduct(w[19], xj)
			o20 += analysisMLPGemmProduct(w[20], xj)
			o21 += analysisMLPGemmProduct(w[21], xj)
			o22 += analysisMLPGemmProduct(w[22], xj)
			o23 += analysisMLPGemmProduct(w[23], xj)
		}
		out[0], out[1], out[2], out[3] = o0, o1, o2, o3
		out[4], out[5], out[6], out[7] = o4, o5, o6, o7
		out[8], out[9], out[10], out[11] = o8, o9, o10, o11
		out[12], out[13], out[14], out[15] = o12, o13, o14, o15
		out[16], out[17], out[18], out[19] = o16, o17, o18, o19
		out[20], out[21], out[22], out[23] = o20, o21, o22, o23
		return
	case 32:
		o0, o1, o2, o3 := out[0], out[1], out[2], out[3]
		o4, o5, o6, o7 := out[4], out[5], out[6], out[7]
		o8, o9, o10, o11 := out[8], out[9], out[10], out[11]
		o12, o13, o14, o15 := out[12], out[13], out[14], out[15]
		o16, o17, o18, o19 := out[16], out[17], out[18], out[19]
		o20, o21, o22, o23 := out[20], out[21], out[22], out[23]
		o24, o25, o26, o27 := out[24], out[25], out[26], out[27]
		o28, o29, o30, o31 := out[28], out[29], out[30], out[31]
		for j := range cols {
			xj := x[j]
			w := weights[j*colStride:]
			o0 += analysisMLPGemmProduct(w[0], xj)
			o1 += analysisMLPGemmProduct(w[1], xj)
			o2 += analysisMLPGemmProduct(w[2], xj)
			o3 += analysisMLPGemmProduct(w[3], xj)
			o4 += analysisMLPGemmProduct(w[4], xj)
			o5 += analysisMLPGemmProduct(w[5], xj)
			o6 += analysisMLPGemmProduct(w[6], xj)
			o7 += analysisMLPGemmProduct(w[7], xj)
			o8 += analysisMLPGemmProduct(w[8], xj)
			o9 += analysisMLPGemmProduct(w[9], xj)
			o10 += analysisMLPGemmProduct(w[10], xj)
			o11 += analysisMLPGemmProduct(w[11], xj)
			o12 += analysisMLPGemmProduct(w[12], xj)
			o13 += analysisMLPGemmProduct(w[13], xj)
			o14 += analysisMLPGemmProduct(w[14], xj)
			o15 += analysisMLPGemmProduct(w[15], xj)
			o16 += analysisMLPGemmProduct(w[16], xj)
			o17 += analysisMLPGemmProduct(w[17], xj)
			o18 += analysisMLPGemmProduct(w[18], xj)
			o19 += analysisMLPGemmProduct(w[19], xj)
			o20 += analysisMLPGemmProduct(w[20], xj)
			o21 += analysisMLPGemmProduct(w[21], xj)
			o22 += analysisMLPGemmProduct(w[22], xj)
			o23 += analysisMLPGemmProduct(w[23], xj)
			o24 += analysisMLPGemmProduct(w[24], xj)
			o25 += analysisMLPGemmProduct(w[25], xj)
			o26 += analysisMLPGemmProduct(w[26], xj)
			o27 += analysisMLPGemmProduct(w[27], xj)
			o28 += analysisMLPGemmProduct(w[28], xj)
			o29 += analysisMLPGemmProduct(w[29], xj)
			o30 += analysisMLPGemmProduct(w[30], xj)
			o31 += analysisMLPGemmProduct(w[31], xj)
		}
		out[0], out[1], out[2], out[3] = o0, o1, o2, o3
		out[4], out[5], out[6], out[7] = o4, o5, o6, o7
		out[8], out[9], out[10], out[11] = o8, o9, o10, o11
		out[12], out[13], out[14], out[15] = o12, o13, o14, o15
		out[16], out[17], out[18], out[19] = o16, o17, o18, o19
		out[20], out[21], out[22], out[23] = o20, o21, o22, o23
		out[24], out[25], out[26], out[27] = o24, o25, o26, o27
		out[28], out[29], out[30], out[31] = o28, o29, o30, o31
		return
	}
	for j := range cols {
		xj := x[j]
		wOff := j * colStride
		w := weights[wOff : wOff+rows]
		i := 0
		for ; i+3 < rows; i += 4 {
			out[i] += analysisMLPGemmProduct(w[i], xj)
			out[i+1] += analysisMLPGemmProduct(w[i+1], xj)
			out[i+2] += analysisMLPGemmProduct(w[i+2], xj)
			out[i+3] += analysisMLPGemmProduct(w[i+3], xj)
		}
		for ; i < rows; i++ {
			out[i] += analysisMLPGemmProduct(w[i], xj)
		}
	}
}

func gemmAccumF32Rows24Pair(out0, out1 []float32, weights []float32, cols, colStride int, x []float32) {
	if cols <= 0 {
		return
	}
	_ = out0[23]
	_ = out1[23]
	_ = x[cols-1]
	_ = weights[(cols-1)*colStride+47]
	a0, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13, a14, a15, a16, a17, a18, a19, a20, a21, a22, a23 := out0[0], out0[1], out0[2], out0[3], out0[4], out0[5], out0[6], out0[7], out0[8], out0[9], out0[10], out0[11], out0[12], out0[13], out0[14], out0[15], out0[16], out0[17], out0[18], out0[19], out0[20], out0[21], out0[22], out0[23]
	b0, b1, b2, b3, b4, b5, b6, b7, b8, b9, b10, b11, b12, b13, b14, b15, b16, b17, b18, b19, b20, b21, b22, b23 := out1[0], out1[1], out1[2], out1[3], out1[4], out1[5], out1[6], out1[7], out1[8], out1[9], out1[10], out1[11], out1[12], out1[13], out1[14], out1[15], out1[16], out1[17], out1[18], out1[19], out1[20], out1[21], out1[22], out1[23]
	for j := range cols {
		xj := x[j]
		w := weights[j*colStride:]
		a0 += analysisMLPGemmProduct(w[0], xj)
		b0 += analysisMLPGemmProduct(w[24], xj)
		a1 += analysisMLPGemmProduct(w[1], xj)
		b1 += analysisMLPGemmProduct(w[25], xj)
		a2 += analysisMLPGemmProduct(w[2], xj)
		b2 += analysisMLPGemmProduct(w[26], xj)
		a3 += analysisMLPGemmProduct(w[3], xj)
		b3 += analysisMLPGemmProduct(w[27], xj)
		a4 += analysisMLPGemmProduct(w[4], xj)
		b4 += analysisMLPGemmProduct(w[28], xj)
		a5 += analysisMLPGemmProduct(w[5], xj)
		b5 += analysisMLPGemmProduct(w[29], xj)
		a6 += analysisMLPGemmProduct(w[6], xj)
		b6 += analysisMLPGemmProduct(w[30], xj)
		a7 += analysisMLPGemmProduct(w[7], xj)
		b7 += analysisMLPGemmProduct(w[31], xj)
		a8 += analysisMLPGemmProduct(w[8], xj)
		b8 += analysisMLPGemmProduct(w[32], xj)
		a9 += analysisMLPGemmProduct(w[9], xj)
		b9 += analysisMLPGemmProduct(w[33], xj)
		a10 += analysisMLPGemmProduct(w[10], xj)
		b10 += analysisMLPGemmProduct(w[34], xj)
		a11 += analysisMLPGemmProduct(w[11], xj)
		b11 += analysisMLPGemmProduct(w[35], xj)
		a12 += analysisMLPGemmProduct(w[12], xj)
		b12 += analysisMLPGemmProduct(w[36], xj)
		a13 += analysisMLPGemmProduct(w[13], xj)
		b13 += analysisMLPGemmProduct(w[37], xj)
		a14 += analysisMLPGemmProduct(w[14], xj)
		b14 += analysisMLPGemmProduct(w[38], xj)
		a15 += analysisMLPGemmProduct(w[15], xj)
		b15 += analysisMLPGemmProduct(w[39], xj)
		a16 += analysisMLPGemmProduct(w[16], xj)
		b16 += analysisMLPGemmProduct(w[40], xj)
		a17 += analysisMLPGemmProduct(w[17], xj)
		b17 += analysisMLPGemmProduct(w[41], xj)
		a18 += analysisMLPGemmProduct(w[18], xj)
		b18 += analysisMLPGemmProduct(w[42], xj)
		a19 += analysisMLPGemmProduct(w[19], xj)
		b19 += analysisMLPGemmProduct(w[43], xj)
		a20 += analysisMLPGemmProduct(w[20], xj)
		b20 += analysisMLPGemmProduct(w[44], xj)
		a21 += analysisMLPGemmProduct(w[21], xj)
		b21 += analysisMLPGemmProduct(w[45], xj)
		a22 += analysisMLPGemmProduct(w[22], xj)
		b22 += analysisMLPGemmProduct(w[46], xj)
		a23 += analysisMLPGemmProduct(w[23], xj)
		b23 += analysisMLPGemmProduct(w[47], xj)
	}
	out0[0], out0[1], out0[2], out0[3] = a0, a1, a2, a3
	out0[4], out0[5], out0[6], out0[7] = a4, a5, a6, a7
	out0[8], out0[9], out0[10], out0[11] = a8, a9, a10, a11
	out0[12], out0[13], out0[14], out0[15] = a12, a13, a14, a15
	out0[16], out0[17], out0[18], out0[19] = a16, a17, a18, a19
	out0[20], out0[21], out0[22], out0[23] = a20, a21, a22, a23
	out1[0], out1[1], out1[2], out1[3] = b0, b1, b2, b3
	out1[4], out1[5], out1[6], out1[7] = b4, b5, b6, b7
	out1[8], out1[9], out1[10], out1[11] = b8, b9, b10, b11
	out1[12], out1[13], out1[14], out1[15] = b12, b13, b14, b15
	out1[16], out1[17], out1[18], out1[19] = b16, b17, b18, b19
	out1[20], out1[21], out1[22], out1[23] = b20, b21, b22, b23
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
	a0, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13, a14, a15, a16, a17, a18, a19, a20, a21, a22, a23 := out0[0], out0[1], out0[2], out0[3], out0[4], out0[5], out0[6], out0[7], out0[8], out0[9], out0[10], out0[11], out0[12], out0[13], out0[14], out0[15], out0[16], out0[17], out0[18], out0[19], out0[20], out0[21], out0[22], out0[23]
	b0, b1, b2, b3, b4, b5, b6, b7, b8, b9, b10, b11, b12, b13, b14, b15, b16, b17, b18, b19, b20, b21, b22, b23 := out1[0], out1[1], out1[2], out1[3], out1[4], out1[5], out1[6], out1[7], out1[8], out1[9], out1[10], out1[11], out1[12], out1[13], out1[14], out1[15], out1[16], out1[17], out1[18], out1[19], out1[20], out1[21], out1[22], out1[23]
	c0, c1, c2, c3, c4, c5, c6, c7, c8, c9, c10, c11, c12, c13, c14, c15, c16, c17, c18, c19, c20, c21, c22, c23 := out2[0], out2[1], out2[2], out2[3], out2[4], out2[5], out2[6], out2[7], out2[8], out2[9], out2[10], out2[11], out2[12], out2[13], out2[14], out2[15], out2[16], out2[17], out2[18], out2[19], out2[20], out2[21], out2[22], out2[23]
	for j := range cols {
		xj := x[j]
		w := weights[j*colStride:]
		a0 += analysisMLPGemmProduct(w[0], xj)
		b0 += analysisMLPGemmProduct(w[24], xj)
		c0 += analysisMLPGemmProduct(w[48], xj)
		a1 += analysisMLPGemmProduct(w[1], xj)
		b1 += analysisMLPGemmProduct(w[25], xj)
		c1 += analysisMLPGemmProduct(w[49], xj)
		a2 += analysisMLPGemmProduct(w[2], xj)
		b2 += analysisMLPGemmProduct(w[26], xj)
		c2 += analysisMLPGemmProduct(w[50], xj)
		a3 += analysisMLPGemmProduct(w[3], xj)
		b3 += analysisMLPGemmProduct(w[27], xj)
		c3 += analysisMLPGemmProduct(w[51], xj)
		a4 += analysisMLPGemmProduct(w[4], xj)
		b4 += analysisMLPGemmProduct(w[28], xj)
		c4 += analysisMLPGemmProduct(w[52], xj)
		a5 += analysisMLPGemmProduct(w[5], xj)
		b5 += analysisMLPGemmProduct(w[29], xj)
		c5 += analysisMLPGemmProduct(w[53], xj)
		a6 += analysisMLPGemmProduct(w[6], xj)
		b6 += analysisMLPGemmProduct(w[30], xj)
		c6 += analysisMLPGemmProduct(w[54], xj)
		a7 += analysisMLPGemmProduct(w[7], xj)
		b7 += analysisMLPGemmProduct(w[31], xj)
		c7 += analysisMLPGemmProduct(w[55], xj)
		a8 += analysisMLPGemmProduct(w[8], xj)
		b8 += analysisMLPGemmProduct(w[32], xj)
		c8 += analysisMLPGemmProduct(w[56], xj)
		a9 += analysisMLPGemmProduct(w[9], xj)
		b9 += analysisMLPGemmProduct(w[33], xj)
		c9 += analysisMLPGemmProduct(w[57], xj)
		a10 += analysisMLPGemmProduct(w[10], xj)
		b10 += analysisMLPGemmProduct(w[34], xj)
		c10 += analysisMLPGemmProduct(w[58], xj)
		a11 += analysisMLPGemmProduct(w[11], xj)
		b11 += analysisMLPGemmProduct(w[35], xj)
		c11 += analysisMLPGemmProduct(w[59], xj)
		a12 += analysisMLPGemmProduct(w[12], xj)
		b12 += analysisMLPGemmProduct(w[36], xj)
		c12 += analysisMLPGemmProduct(w[60], xj)
		a13 += analysisMLPGemmProduct(w[13], xj)
		b13 += analysisMLPGemmProduct(w[37], xj)
		c13 += analysisMLPGemmProduct(w[61], xj)
		a14 += analysisMLPGemmProduct(w[14], xj)
		b14 += analysisMLPGemmProduct(w[38], xj)
		c14 += analysisMLPGemmProduct(w[62], xj)
		a15 += analysisMLPGemmProduct(w[15], xj)
		b15 += analysisMLPGemmProduct(w[39], xj)
		c15 += analysisMLPGemmProduct(w[63], xj)
		a16 += analysisMLPGemmProduct(w[16], xj)
		b16 += analysisMLPGemmProduct(w[40], xj)
		c16 += analysisMLPGemmProduct(w[64], xj)
		a17 += analysisMLPGemmProduct(w[17], xj)
		b17 += analysisMLPGemmProduct(w[41], xj)
		c17 += analysisMLPGemmProduct(w[65], xj)
		a18 += analysisMLPGemmProduct(w[18], xj)
		b18 += analysisMLPGemmProduct(w[42], xj)
		c18 += analysisMLPGemmProduct(w[66], xj)
		a19 += analysisMLPGemmProduct(w[19], xj)
		b19 += analysisMLPGemmProduct(w[43], xj)
		c19 += analysisMLPGemmProduct(w[67], xj)
		a20 += analysisMLPGemmProduct(w[20], xj)
		b20 += analysisMLPGemmProduct(w[44], xj)
		c20 += analysisMLPGemmProduct(w[68], xj)
		a21 += analysisMLPGemmProduct(w[21], xj)
		b21 += analysisMLPGemmProduct(w[45], xj)
		c21 += analysisMLPGemmProduct(w[69], xj)
		a22 += analysisMLPGemmProduct(w[22], xj)
		b22 += analysisMLPGemmProduct(w[46], xj)
		c22 += analysisMLPGemmProduct(w[70], xj)
		a23 += analysisMLPGemmProduct(w[23], xj)
		b23 += analysisMLPGemmProduct(w[47], xj)
		c23 += analysisMLPGemmProduct(w[71], xj)
	}
	out0[0], out0[1], out0[2], out0[3] = a0, a1, a2, a3
	out0[4], out0[5], out0[6], out0[7] = a4, a5, a6, a7
	out0[8], out0[9], out0[10], out0[11] = a8, a9, a10, a11
	out0[12], out0[13], out0[14], out0[15] = a12, a13, a14, a15
	out0[16], out0[17], out0[18], out0[19] = a16, a17, a18, a19
	out0[20], out0[21], out0[22], out0[23] = a20, a21, a22, a23
	out1[0], out1[1], out1[2], out1[3] = b0, b1, b2, b3
	out1[4], out1[5], out1[6], out1[7] = b4, b5, b6, b7
	out1[8], out1[9], out1[10], out1[11] = b8, b9, b10, b11
	out1[12], out1[13], out1[14], out1[15] = b12, b13, b14, b15
	out1[16], out1[17], out1[18], out1[19] = b16, b17, b18, b19
	out1[20], out1[21], out1[22], out1[23] = b20, b21, b22, b23
	out2[0], out2[1], out2[2], out2[3] = c0, c1, c2, c3
	out2[4], out2[5], out2[6], out2[7] = c4, c5, c6, c7
	out2[8], out2[9], out2[10], out2[11] = c8, c9, c10, c11
	out2[12], out2[13], out2[14], out2[15] = c12, c13, c14, c15
	out2[16], out2[17], out2[18], out2[19] = c16, c17, c18, c19
	out2[20], out2[21], out2[22], out2[23] = c20, c21, c22, c23
}
