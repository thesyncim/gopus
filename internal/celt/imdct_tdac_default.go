//go:build !arm64 || nosimd || !goexperiment.simd

package celt

// imdctTDACWindow applies the IMDCT time-domain aliasing-cancellation (TDAC)
// overlap-add windowing of libopus clt_mdct_backward_c(). For each step i in
// [0, count):
//
//	x1 = xsrc[xSrc0-i]
//	x2 = out[yOut0+i]
//	w1 = window[i]
//	w2 = window[wBwd0-i]
//	out[yOut0+i] = mdctMulSubMix(x2, x1, w2, w1)
//	out[xOut0-i] = mdctMulAddMix(x2, x1, w1, w2)
//
// The mix helpers select the fused shape on arm64 and AMD64 v3 and separately
// rounded products on other targets. The SIMD builds supply Go vector versions. Each
// iteration reads its x1 and x2 before writing, so xsrc may alias out.
func imdctTDACWindowScalar(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int) {
	yp := yOut0
	xpOut := xOut0
	xpSrc := xSrc0
	wp1 := 0
	wp2 := wBwd0
	for i := 0; i < count; i++ {
		x1 := xsrc[xpSrc]
		x2 := out[yp]
		w1 := window[wp1]
		w2 := window[wp2]
		out[yp] = mdctMulSubMix(x2, x1, w2, w1)
		out[xpOut] = mdctMulAddMix(x2, x1, w1, w2)
		yp++
		xpOut--
		xpSrc--
		wp1++
		wp2--
	}
}
