package celt

// imdctPostRotateF32FromKissScalar is the clt_mdct_backward_c() post-rotation
// of libopus celt/mdct.c. The float paths on arm64 and AMD64 v3 contract the
// first source product and round the second; mdctMulAddMix/mdctMulSubMix
// reproduce that shape when mdctUseFMALikeMixEnabled is set. Other targets
// keep the separately rounded products.
//
// The rotation reads each fft[] entry exactly once and writes only buf[] (the
// two are distinct, non-aliasing buffers), so it folds the libopus "copy fft
// into buf, then rotate in place" into a single pass that reads the source
// complex pair directly. buf is viewed as float pairs: output pair i is
// buf[2i:2i+2] (libopus yp0) and pair k = n4-1-i is buf[2k:2k+2] (libopus yp1).
func imdctPostRotateF32FromKissScalar(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
	if n4 <= 0 || len(buf) < n2 || len(fft) < n4 {
		return
	}
	in := fft[:n4:n4]
	out := floatPairs(buf[:2*n4])[:len(in)]
	trigA := trig[:len(in)]
	trigB := trig[n4 : n4+len(in)]
	lo := in[:(len(in)+1)>>1]
	for i, v := range lo {
		k := len(in) - 1 - i
		re, im := v.i, v.r
		re2, im2 := in[k].i, in[k].r
		t0, t1 := trigA[i], trigB[i]
		t2, t3 := trigA[k], trigB[k]
		out[i].r = mdctMulAddMix(re, im, t0, t1)
		out[k].i = mdctMulSubMix(re, im, t1, t0)
		out[k].r = mdctMulAddMix(re2, im2, t2, t3)
		out[i].i = mdctMulSubMix(re2, im2, t3, t2)
	}
}
