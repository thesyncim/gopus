package celt

import "github.com/thesyncim/gopus/internal/opusmath"

func orderyForStride(stride int) []int {
	switch stride {
	case 2:
		return orderyTable[0:2]
	case 4:
		return orderyTable[2:6]
	case 8:
		return orderyTable[6:14]
	case 16:
		return orderyTable[14:30]
	default:
		return nil
	}
}

func deinterleaveHadamardStride2Into(dst, src []celtNorm, n0 int) {
	DeinterleaveStereoIntoF32(src, dst[n0:n0<<1], dst[:n0])
}

func interleaveHadamardStride2Into(dst, src []celtNorm, n0 int) {
	InterleaveStereoIntoF32(src[n0:n0<<1], src[:n0], dst)
}

// The fixed-stride Hadamard (de)interleaves slice each group of stride samples
// once, so the per-sample accesses need no bounds checks.
func deinterleaveHadamardStride4Into(dst, src []celtNorm, n0 int) {
	row0 := dst[:n0]
	row1 := dst[n0 : n0*2][:len(row0)]
	row2 := dst[n0*2 : n0*3][:len(row0)]
	row3 := dst[n0*3 : n0*4][:len(row0)]
	for j := range row0 {
		s := src[4*j : 4*j+4 : 4*j+4]
		row3[j] = s[0]
		row0[j] = s[1]
		row2[j] = s[2]
		row1[j] = s[3]
	}
}

func interleaveHadamardStride4Into(dst, src []celtNorm, n0 int) {
	row0 := src[:n0]
	row1 := src[n0 : n0*2][:len(row0)]
	row2 := src[n0*2 : n0*3][:len(row0)]
	row3 := src[n0*3 : n0*4][:len(row0)]
	for j := range row0 {
		d := dst[4*j : 4*j+4 : 4*j+4]
		d[0] = row3[j]
		d[1] = row0[j]
		d[2] = row2[j]
		d[3] = row1[j]
	}
}

func deinterleaveHadamardStride8Into(dst, src []celtNorm, n0 int) {
	row0 := dst[:n0]
	row1 := dst[n0 : n0*2][:len(row0)]
	row2 := dst[n0*2 : n0*3][:len(row0)]
	row3 := dst[n0*3 : n0*4][:len(row0)]
	row4 := dst[n0*4 : n0*5][:len(row0)]
	row5 := dst[n0*5 : n0*6][:len(row0)]
	row6 := dst[n0*6 : n0*7][:len(row0)]
	row7 := dst[n0*7 : n0*8][:len(row0)]
	for j := range row0 {
		s := src[8*j : 8*j+8 : 8*j+8]
		row7[j] = s[0]
		row0[j] = s[1]
		row4[j] = s[2]
		row3[j] = s[3]
		row6[j] = s[4]
		row1[j] = s[5]
		row5[j] = s[6]
		row2[j] = s[7]
	}
}

func interleaveHadamardStride8Into(dst, src []celtNorm, n0 int) {
	row0 := src[:n0]
	row1 := src[n0 : n0*2][:len(row0)]
	row2 := src[n0*2 : n0*3][:len(row0)]
	row3 := src[n0*3 : n0*4][:len(row0)]
	row4 := src[n0*4 : n0*5][:len(row0)]
	row5 := src[n0*5 : n0*6][:len(row0)]
	row6 := src[n0*6 : n0*7][:len(row0)]
	row7 := src[n0*7 : n0*8][:len(row0)]
	for j := range row0 {
		d := dst[8*j : 8*j+8 : 8*j+8]
		d[0] = row7[j]
		d[1] = row0[j]
		d[2] = row4[j]
		d[3] = row3[j]
		d[4] = row6[j]
		d[5] = row1[j]
		d[6] = row5[j]
		d[7] = row2[j]
	}
}

func deinterleaveHadamardStride16Into(dst, src []celtNorm, n0 int) {
	row0 := dst[:n0]
	row1 := dst[n0 : n0*2][:len(row0)]
	row2 := dst[n0*2 : n0*3][:len(row0)]
	row3 := dst[n0*3 : n0*4][:len(row0)]
	row4 := dst[n0*4 : n0*5][:len(row0)]
	row5 := dst[n0*5 : n0*6][:len(row0)]
	row6 := dst[n0*6 : n0*7][:len(row0)]
	row7 := dst[n0*7 : n0*8][:len(row0)]
	row8 := dst[n0*8 : n0*9][:len(row0)]
	row9 := dst[n0*9 : n0*10][:len(row0)]
	row10 := dst[n0*10 : n0*11][:len(row0)]
	row11 := dst[n0*11 : n0*12][:len(row0)]
	row12 := dst[n0*12 : n0*13][:len(row0)]
	row13 := dst[n0*13 : n0*14][:len(row0)]
	row14 := dst[n0*14 : n0*15][:len(row0)]
	row15 := dst[n0*15 : n0*16][:len(row0)]
	for j := range row0 {
		s := src[16*j : 16*j+16 : 16*j+16]
		row15[j] = s[0]
		row0[j] = s[1]
		row8[j] = s[2]
		row7[j] = s[3]
		row12[j] = s[4]
		row3[j] = s[5]
		row11[j] = s[6]
		row4[j] = s[7]
		row14[j] = s[8]
		row1[j] = s[9]
		row9[j] = s[10]
		row6[j] = s[11]
		row13[j] = s[12]
		row2[j] = s[13]
		row10[j] = s[14]
		row5[j] = s[15]
	}
}

func interleaveHadamardStride16Into(dst, src []celtNorm, n0 int) {
	row0 := src[:n0]
	row1 := src[n0 : n0*2][:len(row0)]
	row2 := src[n0*2 : n0*3][:len(row0)]
	row3 := src[n0*3 : n0*4][:len(row0)]
	row4 := src[n0*4 : n0*5][:len(row0)]
	row5 := src[n0*5 : n0*6][:len(row0)]
	row6 := src[n0*6 : n0*7][:len(row0)]
	row7 := src[n0*7 : n0*8][:len(row0)]
	row8 := src[n0*8 : n0*9][:len(row0)]
	row9 := src[n0*9 : n0*10][:len(row0)]
	row10 := src[n0*10 : n0*11][:len(row0)]
	row11 := src[n0*11 : n0*12][:len(row0)]
	row12 := src[n0*12 : n0*13][:len(row0)]
	row13 := src[n0*13 : n0*14][:len(row0)]
	row14 := src[n0*14 : n0*15][:len(row0)]
	row15 := src[n0*15 : n0*16][:len(row0)]
	for j := range row0 {
		d := dst[16*j : 16*j+16 : 16*j+16]
		d[0] = row15[j]
		d[1] = row0[j]
		d[2] = row8[j]
		d[3] = row7[j]
		d[4] = row12[j]
		d[5] = row3[j]
		d[6] = row11[j]
		d[7] = row4[j]
		d[8] = row14[j]
		d[9] = row1[j]
		d[10] = row9[j]
		d[11] = row6[j]
		d[12] = row13[j]
		d[13] = row2[j]
		d[14] = row10[j]
		d[15] = row5[j]
	}
}

func deinterleaveHadamard(x []celtNorm, n0, stride int, hadamard bool) {
	deinterleaveHadamardScratchBuf(x, n0, stride, hadamard, nil, nil)
}

func deinterleaveHadamardInto(dst, src []celtNorm, n0, stride int, hadamard bool) {
	n := n0 * stride
	dst = dst[:n]
	src = src[:n]
	if hadamard {
		switch stride {
		case 2:
			deinterleaveHadamardStride2Into(dst, src, n0)
		case 4:
			deinterleaveHadamardStride4Into(dst, src, n0)
		case 8:
			deinterleaveHadamardStride8Into(dst, src, n0)
		case 16:
			deinterleaveHadamardStride16Into(dst, src, n0)
		default:
			ordery := orderyForStride(stride)
			for i := range stride {
				row := ordery[i] * n0
				for j := range n0 {
					dst[row+j] = src[j*stride+i]
				}
			}
		}
		return
	}
	switch stride {
	case 2:
		DeinterleaveStereoIntoF32(src, dst[:n0], dst[n0:n])
	case 3:
		n1 := n0
		n2 := n0 << 1
		for j := range n0 {
			base := j * 3
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
		}
	case 4:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		for j := range n0 {
			base := j << 2
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
			dst[n3+j] = src[base+3]
		}
	case 8:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		for j := range n0 {
			base := j << 3
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
			dst[n3+j] = src[base+3]
			dst[n4+j] = src[base+4]
			dst[n5+j] = src[base+5]
			dst[n6+j] = src[base+6]
			dst[n7+j] = src[base+7]
		}
	case 12:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		n8 := n0 << 3
		n9 := n8 + n0
		n10 := n8 + n2
		n11 := n8 + n3
		for j := range n0 {
			base := j * 12
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
			dst[n3+j] = src[base+3]
			dst[n4+j] = src[base+4]
			dst[n5+j] = src[base+5]
			dst[n6+j] = src[base+6]
			dst[n7+j] = src[base+7]
			dst[n8+j] = src[base+8]
			dst[n9+j] = src[base+9]
			dst[n10+j] = src[base+10]
			dst[n11+j] = src[base+11]
		}
	case 16:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		n8 := n0 << 3
		n9 := n8 + n0
		n10 := n8 + n2
		n11 := n8 + n3
		n12 := n0 * 12
		n13 := n12 + n0
		n14 := n12 + n2
		n15 := n12 + n3
		for j := range n0 {
			base := j << 4
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
			dst[n3+j] = src[base+3]
			dst[n4+j] = src[base+4]
			dst[n5+j] = src[base+5]
			dst[n6+j] = src[base+6]
			dst[n7+j] = src[base+7]
			dst[n8+j] = src[base+8]
			dst[n9+j] = src[base+9]
			dst[n10+j] = src[base+10]
			dst[n11+j] = src[base+11]
			dst[n12+j] = src[base+12]
			dst[n13+j] = src[base+13]
			dst[n14+j] = src[base+14]
			dst[n15+j] = src[base+15]
		}
	case 6:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		for j := range n0 {
			base := j * 6
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
			dst[n3+j] = src[base+3]
			dst[n4+j] = src[base+4]
			dst[n5+j] = src[base+5]
		}
	default:
		for i := range stride {
			row := i * n0
			for j := range n0 {
				dst[row+j] = src[j*stride+i]
			}
		}
	}
}

func deinterleaveHadamardScratchBuf(x []celtNorm, n0, stride int, hadamard bool, decScratch *bandDecodeScratch, encScratch *bandEncodeScratch) {
	n := n0 * stride
	var tmp []celtNorm
	if decScratch != nil {
		tmp = decScratch.ensureHadamardTmpNorm(n)
	} else if encScratch != nil {
		tmp = encScratch.ensureHadamardTmpNorm(n)
	} else {
		tmp = make([]celtNorm, n)
	}
	deinterleaveHadamardInto(tmp, x, n0, stride, hadamard)
	copy(x, tmp)
}

func deinterleaveHadamardIntoNorm(dst, src []celtNorm, n0, stride int, hadamard bool) {
	// Strides without a Hadamard ordering fall back to the plain deinterleave.
	if hadamard && len(orderyForStride(stride)) < stride {
		hadamard = false
	}
	deinterleaveHadamardInto(dst, src, n0, stride, hadamard)
}

func deinterleaveHadamardScratchBufNorm(x []celtNorm, n0, stride int, hadamard bool, decScratch *bandDecodeScratch, encScratch *bandEncodeScratch) {
	n := n0 * stride
	var tmp []celtNorm
	if decScratch != nil {
		tmp = decScratch.ensureHadamardTmpNorm(n)
	} else if encScratch != nil {
		tmp = encScratch.ensureHadamardTmpNorm(n)
	} else {
		tmp = make([]celtNorm, n)
	}
	deinterleaveHadamardIntoNorm(tmp, x, n0, stride, hadamard)
	copy(x, tmp)
}

func interleaveHadamard(x []celtNorm, n0, stride int, hadamard bool) {
	interleaveHadamardScratchBuf(x, n0, stride, hadamard, nil, nil)
}

func interleaveHadamardInto(dst, src []celtNorm, n0, stride int, hadamard bool) {
	n := n0 * stride
	dst = dst[:n]
	src = src[:n]
	if hadamard {
		switch stride {
		case 2:
			interleaveHadamardStride2Into(dst, src, n0)
		case 4:
			interleaveHadamardStride4Into(dst, src, n0)
		case 8:
			interleaveHadamardStride8Into(dst, src, n0)
		case 16:
			interleaveHadamardStride16Into(dst, src, n0)
		default:
			ordery := orderyForStride(stride)
			for i := range stride {
				row := ordery[i] * n0
				for j := range n0 {
					dst[j*stride+i] = src[row+j]
				}
			}
		}
		return
	}
	switch stride {
	case 2:
		InterleaveStereoIntoF32(src[:n0], src[n0:n], dst)
	case 3:
		n1 := n0
		n2 := n0 << 1
		for j := range n0 {
			base := j * 3
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
		}
	case 4:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		for j := range n0 {
			base := j << 2
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
			dst[base+3] = src[n3+j]
		}
	case 8:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		for j := range n0 {
			base := j << 3
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
			dst[base+3] = src[n3+j]
			dst[base+4] = src[n4+j]
			dst[base+5] = src[n5+j]
			dst[base+6] = src[n6+j]
			dst[base+7] = src[n7+j]
		}
	case 12:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		n8 := n0 << 3
		n9 := n8 + n0
		n10 := n8 + n2
		n11 := n8 + n3
		for j := range n0 {
			base := j * 12
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
			dst[base+3] = src[n3+j]
			dst[base+4] = src[n4+j]
			dst[base+5] = src[n5+j]
			dst[base+6] = src[n6+j]
			dst[base+7] = src[n7+j]
			dst[base+8] = src[n8+j]
			dst[base+9] = src[n9+j]
			dst[base+10] = src[n10+j]
			dst[base+11] = src[n11+j]
		}
	case 16:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		n8 := n0 << 3
		n9 := n8 + n0
		n10 := n8 + n2
		n11 := n8 + n3
		n12 := n0 * 12
		n13 := n12 + n0
		n14 := n12 + n2
		n15 := n12 + n3
		for j := range n0 {
			base := j << 4
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
			dst[base+3] = src[n3+j]
			dst[base+4] = src[n4+j]
			dst[base+5] = src[n5+j]
			dst[base+6] = src[n6+j]
			dst[base+7] = src[n7+j]
			dst[base+8] = src[n8+j]
			dst[base+9] = src[n9+j]
			dst[base+10] = src[n10+j]
			dst[base+11] = src[n11+j]
			dst[base+12] = src[n12+j]
			dst[base+13] = src[n13+j]
			dst[base+14] = src[n14+j]
			dst[base+15] = src[n15+j]
		}
	case 6:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		for j := range n0 {
			base := j * 6
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
			dst[base+3] = src[n3+j]
			dst[base+4] = src[n4+j]
			dst[base+5] = src[n5+j]
		}
	default:
		for i := range stride {
			row := i * n0
			for j := range n0 {
				dst[j*stride+i] = src[row+j]
			}
		}
	}
}

func interleaveHadamardScratchBuf(x []celtNorm, n0, stride int, hadamard bool, decScratch *bandDecodeScratch, encScratch *bandEncodeScratch) {
	n := n0 * stride
	var tmp []celtNorm
	if decScratch != nil {
		tmp = decScratch.ensureHadamardTmpNorm(n)
	} else if encScratch != nil {
		tmp = encScratch.ensureHadamardTmpNorm(n)
	} else {
		tmp = make([]celtNorm, n)
	}
	interleaveHadamardInto(tmp, x, n0, stride, hadamard)
	copy(x, tmp)
}

func haar1(x []celtNorm, n0, stride int) {
	n0 >>= 1
	if n0 <= 0 || stride <= 0 {
		return
	}

	const invSqrt2 = float32(0.7071067811865476)

	step := stride * 2
	// BCE hint: maximum index accessed is (stride-1) + stride + (n0-1)*step
	maxIdx := stride - 1 + stride + (n0-1)*step
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	switch stride {
	case 1:
		haar1Stride1(x[:2*n0:2*n0], n0)
		return
	case 2:
		haar1Stride2(x[:4*n0:4*n0], n0)
		return
	case 4:
		haar1Stride4(x[:8*n0:8*n0], n0)
		return
	}
	for i := range stride {
		idx0 := i
		idx1 := i + stride
		for j := 0; j < n0; j++ {
			haar1PairNorm(x, idx0, idx1, invSqrt2)
			idx0 += step
			idx1 += step
		}
	}
}

func haar1PairNorm(x []celtNorm, idx0, idx1 int, invSqrt2 float32) {
	sum, diff := haar1PairValues(invSqrt2, float32(x[idx0]), float32(x[idx1]))
	x[idx0] = celtNorm(sum)
	x[idx1] = celtNorm(diff)
}

func expRotation1(x []celtNorm, length, stride int, c, s opusVal16) {
	expRotation1Norm(x, length, stride, c, s)
}

func expRotation1Norm(x []celtNorm, length, stride int, c, s opusVal16) {
	if length <= 0 {
		return
	}
	x = x[:length:length]
	if stride <= 0 || stride >= length {
		return
	}
	// With stride >= 4 four consecutive indices belong to four independent
	// rotation chains, so the Go SIMD builds run both passes 4-wide
	// (bit-identical per element); the scalar loops stay the nosimd oracle
	// and the stride<4 path.
	if expRotationUsesSIMD && stride >= 4 {
		expRotation1StrideSIMD(x, length, stride, c, s)
		return
	}
	expRotation1NormScalar(x, length, stride, c, s)
}

func expRotation1NormScalar(x []celtNorm, length, stride int, c, s opusVal16) {
	if stride == 1 {
		expRotation1Stride1(x, length, float32(c), float32(s))
		return
	}
	// xs[i] aliases x[i+stride] and has exactly length-stride elements — the
	// trip count of the forward pass — so every access below is bounds-check
	// free. The rotation itself is a serial cascade (each pair reads the
	// previous pair's write), so only loop overhead is reducible here.
	xs := x[stride:]
	c32 := float32(c)
	s32 := float32(s)
	ms32 := -s32

	for i := range xs {
		x1 := float32(x[i])
		x2 := float32(xs[i])
		xs[i] = celtNorm(expRotationMac32(c32, x2, s32, x1))
		x[i] = celtNorm(expRotationMac32(c32, x1, ms32, x2))
	}
	if n2 := length - 2*stride; n2 > 0 {
		xb, xsb := x[:n2], xs[:n2]
		for i := len(xsb) - 1; i >= 0; i-- {
			x1 := float32(xb[i])
			x2 := float32(xsb[i])
			xsb[i] = celtNorm(expRotationMac32(c32, x2, s32, x1))
			xb[i] = celtNorm(expRotationMac32(c32, x1, ms32, x2))
		}
	}
}

// expRotation1Stride1 is exp_rotation1 for stride 1. Each step of either
// pass rewrites the element the next step reads, so that element stays in a
// register instead of taking a store-to-load round trip through x; every
// element sees the same operations as in the strided loops.
func expRotation1Stride1(x []celtNorm, length int, c, s float32) {
	if length < 2 {
		return
	}
	x = x[:length:length]
	ms := -s
	// Each product pair is expRotationMac32 written out, a*b + float32(c*d),
	// so the loops carry no inlined call sites.
	// Forward: step i rotates (x[i], x[i+1]); x[i+1] is the next step's x1.
	x1 := float32(x[0])
	for i := 1; i < length; i++ {
		x2 := float32(x[i])
		x[i-1] = celtNorm(c*x1 + float32(ms*x2))
		x1 = c*x2 + float32(s*x1)
	}
	x[length-1] = celtNorm(x1)
	// Backward: step j-1 rotates (x[j-1], x[j]) for j = length-2 down to 1;
	// x[j-1] is the next step's x2.
	if length < 3 {
		return
	}
	x2 := float32(x[length-2])
	for j := length - 2; j >= 1; j-- {
		x1 := float32(x[j-1])
		x[j] = celtNorm(c*x2 + float32(s*x1))
		x2 = c*x1 + float32(ms*x2)
	}
	x[0] = celtNorm(x2)
}

// expRotationMac32 is exp_rotation1's MAC16_16(MULT16_16(a, b), c, d) in
// the float build: c*d rounds on its own and a*b may contract with the add
// (fma32).
func expRotationMac32(a, b, c, d float32) float32 {
	return a*b + float32(c*d)
}

func expRotation(x []celtNorm, length, dir, stride, k, spread int) {
	if 2*k >= length || spread == spreadNone {
		return
	}
	c, s, ok := expRotationCoefficients(length, k, spread)
	if !ok {
		spreadFactor := expRotationSpreadFactors[spread-1]
		gain := float32(length) / float32(length+spreadFactor*k)
		theta := noFMA32Mul(0.5, noFMA32Mul(gain, gain))
		c = opusVal16(opusmath.CELTCosNormF32(theta))
		s = opusVal16(opusmath.CELTCosNormF32(noFMA32Sub(1, theta)))
	}

	stride2 := 0
	if length >= 8*stride {
		stride2 = 1
		for (stride2*stride2+stride2)*stride+(stride>>2) < length {
			stride2++
		}
	}
	length = celtUdiv(length, stride)
	for i := range stride {
		off := i * length
		if dir < 0 {
			if stride2 != 0 {
				expRotation1(x[off:], length, stride2, s, c)
			}
			expRotation1(x[off:], length, 1, c, s)
		} else {
			expRotation1(x[off:], length, 1, c, -s)
			if stride2 != 0 {
				expRotation1(x[off:], length, stride2, s, -c)
			}
		}
	}
}

// expRotationNorm is libopus exp_rotation(). The early return is kept in this
// small function so it inlines into the callers; most bands take it.
func expRotationNorm(x []celtNorm, length, dir, stride, k, spread int) {
	if 2*k >= length || spread == spreadNone {
		return
	}
	expRotationNormApply(x, length, dir, stride, k, spread)
}

// expRotationNormApply is the exp_rotation() body for bands that rotate.
func expRotationNormApply(x []celtNorm, length, dir, stride, k, spread int) {
	c, s, ok := expRotationCoefficients(length, k, spread)
	if !ok {
		spreadFactor := expRotationSpreadFactors[spread-1]
		gain := float32(length) / float32(length+spreadFactor*k)
		theta := noFMA32Mul(0.5, noFMA32Mul(gain, gain))
		c = opusVal16(opusmath.CELTCosNormF32(theta))
		s = opusVal16(opusmath.CELTCosNormF32(noFMA32Sub(1, theta)))
	}

	stride2 := 0
	if length >= 8*stride {
		stride2 = 1
		for (stride2*stride2+stride2)*stride+(stride>>2) < length {
			stride2++
		}
	}
	length = celtUdiv(length, stride)
	for i := range stride {
		off := i * length
		if dir < 0 {
			if stride2 != 0 {
				expRotation1Norm(x[off:], length, stride2, s, c)
			}
			expRotation1Norm(x[off:], length, 1, c, s)
		} else {
			expRotation1Norm(x[off:], length, 1, c, -s)
			if stride2 != 0 {
				expRotation1Norm(x[off:], length, stride2, s, -c)
			}
		}
	}
}
