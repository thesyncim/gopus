package celt

func imdctPostRotateF32FromKissScalar(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
	if len(buf) < n2 || len(fft) < n4 {
		return
	}
	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}
	_ = buf[n2-1]
	_ = fft[n4-1]
	_ = trig[n2-1]

	// Match libopus celt/mdct.c clt_mdct_backward_c() post-rotation. On arm64
	// the float path contracts re*t0 + im*t1 into single-rounding FMADDS;
	// mdctMulAddMix/mdctMulSubMix reproduce that fused shape when
	// mdctUseFMALikeMixEnabled is set (arm64) and stay split elsewhere, so this
	// portable path matches the assembly rotation on nosimd/arm64 and the
	// scalar reference on other targets.
	//
	// The rotation reads each fft[] entry exactly once and writes only buf[]
	// (the two are distinct, non-aliasing buffers), so it folds the libopus
	// "copy fft into buf, then rotate in place" into a single pass that reads
	// the source complex pair directly — half the memory traffic, same arith.
	// Output pair i is buf[2i:2i+2] and pair k = n4-1-i is buf[2k:2k+2]
	// (libopus yp0 and yp1).
	// fft is read through its float view: pair i is in[2i] = r, in[2i+1] = i.
	// lo and hi step by two on their own, like libopus yp0 and yp1, so every
	// access is a scaled float index.
	in := kissFloats(fft[:n4])
	trigA := trig[:n4]
	trigB := trig[n4 : 2*n4]
	out := buf[:len(in)]
	hi := len(in) - 2
	for i, lo := 0, 0; i < limit && lo+1 < len(in); i, lo = i+1, lo+2 {
		k := n4 - 1 - i
		if hi < 0 || hi+1 >= len(in) {
			break
		}
		re, im := in[lo+1], in[lo]
		re2, im2 := in[hi+1], in[hi]
		t0, t1 := trigA[i], trigB[i]
		t2, t3 := trigA[k], trigB[k]
		out[lo] = mdctMulAddMix(re, im, t0, t1)
		out[hi+1] = mdctMulSubMix(re, im, t1, t0)
		out[hi] = mdctMulAddMix(re2, im2, t2, t3)
		out[lo+1] = mdctMulSubMix(re2, im2, t3, t2)
		hi -= 2
	}
}
