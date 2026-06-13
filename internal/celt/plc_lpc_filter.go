package celt

func xcorrKernel4Float32(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	// The kernel reads x[0:length] and y[0:length+3]. Slicing to those exact
	// bounds and advancing the slices (rather than indexing off a stride-4
	// counter, which prove cannot reason about) eliminates every per-access
	// bounds check in the unrolled body. The walk and the FP expressions are
	// unchanged, so results stay bit-identical per arch.
	x = x[:length]
	y = y[:length+3]
	// Accumulate in registers: with sum as a pointer every sum[k]+= is a
	// memory read-modify-write the compiler cannot keep in a register. Locals
	// run the identical IEEE adds in the identical order, written back once.
	s0, s1, s2, s3 := sum[0], sum[1], sum[2], sum[3]
	var y3 float32
	y0 := y[0]
	y1 := y[1]
	y2 := y[2]
	for len(x) >= 4 && len(y) >= 7 {
		tmp := x[0]
		y3 = y[3]
		s0 += tmp * y0
		s1 += tmp * y1
		s2 += tmp * y2
		s3 += tmp * y3

		tmp = x[1]
		y0 = y[4]
		s0 += tmp * y1
		s1 += tmp * y2
		s2 += tmp * y3
		s3 += tmp * y0

		tmp = x[2]
		y1 = y[5]
		s0 += tmp * y2
		s1 += tmp * y3
		s2 += tmp * y0
		s3 += tmp * y1

		tmp = x[3]
		y2 = y[6]
		s0 += tmp * y3
		s1 += tmp * y0
		s2 += tmp * y1
		s3 += tmp * y2

		x = x[4:]
		y = y[4:]
	}
	if len(x) >= 1 && len(y) >= 4 {
		tmp := x[0]
		y3 = y[3]
		s0 += tmp * y0
		s1 += tmp * y1
		s2 += tmp * y2
		s3 += tmp * y3
		x = x[1:]
		y = y[1:]
	}
	if len(x) >= 1 && len(y) >= 4 {
		tmp := x[0]
		y0 = y[3]
		s0 += tmp * y1
		s1 += tmp * y2
		s2 += tmp * y3
		s3 += tmp * y0
		x = x[1:]
		y = y[1:]
	}
	if len(x) >= 1 && len(y) >= 4 {
		tmp := x[0]
		y1 = y[3]
		s0 += tmp * y2
		s1 += tmp * y3
		s2 += tmp * y0
		s3 += tmp * y1
	}
	sum[0] = s0
	sum[1] = s1
	sum[2] = s2
	sum[3] = s3
}

// xcorrKernel8Float32 computes eight simultaneous cross-correlation lags in a
// single pass over x, reading x[0:length] and y[0:length+7]. Calling this in
// the outer loop of pitchXCorrFloat32 halves the number of function calls and
// lets the compiler interleave all eight accumulator chains for better ILP.
//
// Accumulation order per lag matches back-to-back xcorrKernel4Float32 calls for
// the same lag pairs, so the float32 results are bit-identical.
func xcorrKernel8Float32(x, y []float32, sum *[8]float32, length int) {
	if length <= 0 {
		return
	}
	x = x[:length]
	y = y[:length+7]
	s0, s1, s2, s3, s4, s5, s6, s7 := sum[0], sum[1], sum[2], sum[3], sum[4], sum[5], sum[6], sum[7]
	for len(x) >= 4 && len(y) >= 11 {
		x0, x1, x2, x3 := x[0], x[1], x[2], x[3]
		y0, y1, y2, y3, y4, y5, y6, y7, y8, y9, y10 := y[0], y[1], y[2], y[3], y[4], y[5], y[6], y[7], y[8], y[9], y[10]

		s0 += x0 * y0
		s1 += x0 * y1
		s2 += x0 * y2
		s3 += x0 * y3
		s4 += x0 * y4
		s5 += x0 * y5
		s6 += x0 * y6
		s7 += x0 * y7

		s0 += x1 * y1
		s1 += x1 * y2
		s2 += x1 * y3
		s3 += x1 * y4
		s4 += x1 * y5
		s5 += x1 * y6
		s6 += x1 * y7
		s7 += x1 * y8

		s0 += x2 * y2
		s1 += x2 * y3
		s2 += x2 * y4
		s3 += x2 * y5
		s4 += x2 * y6
		s5 += x2 * y7
		s6 += x2 * y8
		s7 += x2 * y9

		s0 += x3 * y3
		s1 += x3 * y4
		s2 += x3 * y5
		s3 += x3 * y6
		s4 += x3 * y7
		s5 += x3 * y8
		s6 += x3 * y9
		s7 += x3 * y10

		x = x[4:]
		y = y[4:]
	}
	for len(x) >= 1 && len(y) >= 8 {
		t := x[0]
		s0 += t * y[0]
		s1 += t * y[1]
		s2 += t * y[2]
		s3 += t * y[3]
		s4 += t * y[4]
		s5 += t * y[5]
		s6 += t * y[6]
		s7 += t * y[7]
		x = x[1:]
		y = y[1:]
	}
	sum[0] = s0
	sum[1] = s1
	sum[2] = s2
	sum[3] = s3
	sum[4] = s4
	sum[5] = s5
	sum[6] = s6
	sum[7] = s7
}

func celtFIRFloat32(dst []celtSig, exc []celtSig, start, length int, lpc []float32) {
	const ord = celtPLCLPCOrder
	if length <= 0 || len(dst) < length || start-ord < 0 || start+length > len(exc) || len(lpc) < ord {
		return
	}
	var rnum [ord]float32
	for i := range ord {
		rnum[i] = float32(lpc[ord-1-i])
	}
	i := 0
	for ; i < length-7; i += 8 {
		sum := [8]float32{
			exc[start+i], exc[start+i+1], exc[start+i+2], exc[start+i+3],
			exc[start+i+4], exc[start+i+5], exc[start+i+6], exc[start+i+7],
		}
		xcorrKernel8Float32(rnum[:], exc[start+i-ord:], &sum, ord)
		dst[i] = celtSig(sum[0])
		dst[i+1] = celtSig(sum[1])
		dst[i+2] = celtSig(sum[2])
		dst[i+3] = celtSig(sum[3])
		dst[i+4] = celtSig(sum[4])
		dst[i+5] = celtSig(sum[5])
		dst[i+6] = celtSig(sum[6])
		dst[i+7] = celtSig(sum[7])
	}
	for ; i < length-3; i += 4 {
		sum := [4]float32{
			exc[start+i],
			exc[start+i+1],
			exc[start+i+2],
			exc[start+i+3],
		}
		xcorrKernel4Float32(rnum[:], exc[start+i-ord:], &sum, ord)
		dst[i] = celtSig(sum[0])
		dst[i+1] = celtSig(sum[1])
		dst[i+2] = celtSig(sum[2])
		dst[i+3] = celtSig(sum[3])
	}
	for ; i < length; i++ {
		sum := float32(exc[start+i])
		for j := range ord {
			sum += rnum[j] * exc[start+i+j-ord]
		}
		dst[i] = celtSig(sum)
	}
}

func (d *Decoder) celtIIRFloat32(dst []celtSig, hist []celtSig, lpc []float32, length int) {
	const ord = celtPLCLPCOrder
	if length <= 0 || len(dst) < length || len(hist) < plcDecodeBufferSize || len(lpc) < ord {
		return
	}
	var rden [ord]float32
	var mem [ord]float32
	for i := range ord {
		rden[i] = float32(lpc[ord-1-i])
		mem[i] = float32(hist[plcDecodeBufferSize-1-i])
	}
	y := ensureFloat32Slice(&d.scratchPLCIIRY, length+ord)
	for i := range ord {
		y[i] = -mem[ord-i-1]
	}
	clear(y[ord:])

	i := 0
	for ; i < length-3; i += 4 {
		sum := [4]float32{
			float32(dst[i]),
			float32(dst[i+1]),
			float32(dst[i+2]),
			float32(dst[i+3]),
		}
		xcorrKernel4Float32(rden[:], y[i:], &sum, ord)

		y[i+ord] = -sum[0]
		dst[i] = celtSig(sum[0])

		sum[1] += y[i+ord] * float32(lpc[0])
		y[i+ord+1] = -sum[1]
		dst[i+1] = celtSig(sum[1])

		sum[2] += y[i+ord+1] * float32(lpc[0])
		sum[2] += y[i+ord] * float32(lpc[1])
		y[i+ord+2] = -sum[2]
		dst[i+2] = celtSig(sum[2])

		sum[3] += y[i+ord+2] * float32(lpc[0])
		sum[3] += y[i+ord+1] * float32(lpc[1])
		sum[3] += y[i+ord] * float32(lpc[2])
		y[i+ord+3] = -sum[3]
		dst[i+3] = celtSig(sum[3])
	}
	for ; i < length; i++ {
		sum := float32(dst[i])
		for j := range ord {
			sum -= rden[j] * y[i+j]
		}
		y[i+ord] = sum
		dst[i] = celtSig(sum)
	}
}
