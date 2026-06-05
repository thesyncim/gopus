package celt

func xcorrKernel4Float32(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	// The kernel reads x[0:length] and y[0:length+3]; hoisting those bounds
	// lets the compiler drop the per-access checks in the inner loop. The
	// original walks the same indices, so this cannot panic where it would not.
	_ = x[length-1]
	_ = y[length+2]
	// Accumulate in registers: with sum as a pointer every sum[k]+= is a
	// memory read-modify-write the compiler cannot keep in a register. Locals
	// run the identical IEEE adds in the identical order, written back once.
	s0, s1, s2, s3 := sum[0], sum[1], sum[2], sum[3]
	xi := 0
	yi := 0
	var y3 float32
	y0 := y[yi]
	yi++
	y1 := y[yi]
	yi++
	y2 := y[yi]
	yi++
	j := 0
	for ; j < length-3; j += 4 {
		tmp := x[xi]
		xi++
		y3 = y[yi]
		yi++
		s0 += tmp * y0
		s1 += tmp * y1
		s2 += tmp * y2
		s3 += tmp * y3

		tmp = x[xi]
		xi++
		y0 = y[yi]
		yi++
		s0 += tmp * y1
		s1 += tmp * y2
		s2 += tmp * y3
		s3 += tmp * y0

		tmp = x[xi]
		xi++
		y1 = y[yi]
		yi++
		s0 += tmp * y2
		s1 += tmp * y3
		s2 += tmp * y0
		s3 += tmp * y1

		tmp = x[xi]
		xi++
		y2 = y[yi]
		yi++
		s0 += tmp * y3
		s1 += tmp * y0
		s2 += tmp * y1
		s3 += tmp * y2
	}
	if j < length {
		j++
		tmp := x[xi]
		xi++
		y3 = y[yi]
		yi++
		s0 += tmp * y0
		s1 += tmp * y1
		s2 += tmp * y2
		s3 += tmp * y3
	}
	if j < length {
		j++
		tmp := x[xi]
		xi++
		y0 = y[yi]
		yi++
		s0 += tmp * y1
		s1 += tmp * y2
		s2 += tmp * y3
		s3 += tmp * y0
	}
	if j < length {
		tmp := x[xi]
		y1 = y[yi]
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
