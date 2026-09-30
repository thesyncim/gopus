package celt

import "unsafe"

// imdctPostRotateF32FromKissScalar is the clt_mdct_backward_c() post-rotation
// of libopus celt/mdct.c. The float paths on arm64 and AMD64 v3 contract the
// first source product and round the second; mdctMulAddMix/mdctMulSubMix
// reproduce that shape when mdctUseFMALikeMixEnabled is set. Other targets
// keep the separately rounded products.
//
// The rotation reads each fft[] entry exactly once and writes only buf[] (the
// two are distinct, non-aliasing buffers), so it folds the libopus "copy fft
// into buf, then rotate in place" into a single pass that reads the source
// complex pair directly. Output pair i is buf[2i:2i+2] (libopus yp0) and pair
// k = n4-1-i is buf[2k:2k+2] (libopus yp1).
func imdctPostRotateF32FromKissScalar(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
	if n4 <= 0 || len(buf) < n2 || len(fft) < n4 {
		return
	}
	imdctPostRotateF32FromKissOffsets(buf, fft, trig, n4)
}

// imdctPostRotateF32FromKissOffsets is the imdctPostRotateF32FromKissScalar
// loop with both complex runs addressed by byte offsets: lo walks pair i up
// from the start and hi walks pair n4-1-i down from the end of the checked
// fft[:n4] and buf[:2*n4], so every load and store folds its address. Both
// pairs are read before either is written, so pair i == k needs no care.
func imdctPostRotateF32FromKissOffsets(buf []float32, fft []kissCpx, trig []float32, n4 int) {
	in := unsafe.Pointer(unsafe.SliceData(fft[:n4]))
	out := unsafe.Pointer(unsafe.SliceData(buf[:2*n4]))
	trigA := trig[:n4]
	trigB := trig[n4 : 2*n4][:len(trigA)]
	lo := uintptr(0)
	hi := uintptr(n4-1) * 8
	for i := range (len(trigA) + 1) >> 1 {
		k := len(trigA) - 1 - i
		re, im := *(*float32)(unsafe.Add(in, lo+4)), *(*float32)(unsafe.Add(in, lo))
		re2, im2 := *(*float32)(unsafe.Add(in, hi+4)), *(*float32)(unsafe.Add(in, hi))
		t0, t1 := trigA[i], trigB[i]
		t2, t3 := trigA[k], trigB[k]
		*(*float32)(unsafe.Add(out, lo)) = mdctMulAddMix(re, im, t0, t1)
		*(*float32)(unsafe.Add(out, hi+4)) = mdctMulSubMix(re, im, t1, t0)
		*(*float32)(unsafe.Add(out, hi)) = mdctMulAddMix(re2, im2, t2, t3)
		*(*float32)(unsafe.Add(out, lo+4)) = mdctMulSubMix(re2, im2, t3, t2)
		lo += 8
		hi -= 8
	}
}
