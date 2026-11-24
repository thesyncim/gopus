//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var celtFIR5UsesAVX = archsimd.X86.AVX()

// celtFIR5F32 is libopus celt_fir5() applied in place to x. The filter has no
// feedback: every output is x[i] plus the five taps on the original inputs
// x[i-1] ... x[i-5]. The AVX path computes four outputs per vector in the
// selected libopus MAC order and walks from the end of x, so the inputs a
// vector reads are not yet overwritten.
func celtFIR5F32(x []float32, num [5]float32) {
	if !celtFIR5UsesAVX || (len(x) < 9 && !celtFIR5UsesFMA) {
		celtFIR5Scalar(x, num)
		return
	}
	celtFIR5AVX(x, num)
}

//go:noinline
func celtFIR5AVX(x []float32, num [5]float32) {
	n0 := broadcastF32x4Arch(num[0])
	n1 := broadcastF32x4Arch(num[1])
	n2 := broadcastF32x4Arch(num[2])
	n3 := broadcastF32x4Arch(num[3])
	n4 := broadcastF32x4Arch(num[4])
	// Vector i covers outputs [i, i+4) and reads x[i-5 : i+4], so i >= 5.
	i := len(x) - 4
	for ; i >= 5; i -= 4 {
		w := x[i-5 : i+4 : i+4]
		s := archsimd.LoadFloat32x4Array((*[4]float32)(w[5:9]))
		s = celtFIR5Accumulate(s, n0, archsimd.LoadFloat32x4Array((*[4]float32)(w[4:8])))
		s = celtFIR5Accumulate(s, n1, archsimd.LoadFloat32x4Array((*[4]float32)(w[3:7])))
		s = celtFIR5Accumulate(s, n2, archsimd.LoadFloat32x4Array((*[4]float32)(w[2:6])))
		s = celtFIR5Accumulate(s, n3, archsimd.LoadFloat32x4Array((*[4]float32)(w[1:5])))
		s = celtFIR5Accumulate(s, n4, archsimd.LoadFloat32x4Array((*[4]float32)(w[0:4])))
		s.StoreArray((*[4]float32)(w[5:9]))
	}
	celtFIR5Head(x[:i+4], num)
}

// pitchDownsample2 is the factor-2 pitch_downsample() decimation of outputs
// [1, len(dst)); see pitchDownsample2Scalar. The AVX path produces four
// outputs per step and leaves the tail to the scalar loop.
func pitchDownsample2(dst, x0, x1 []float32) {
	start := 1
	if celtFIR5UsesAVX && len(dst) > 5 {
		start = pitchDownsample2AVX(dst, x0, x1)
	}
	pitchDownsample2Scalar(dst, x0, x1, start)
}

// pitchDownsample2AVX computes outputs from 1 upward four at a time while
// i+4 < len(dst) and returns the first output it leaves to the scalar loop.
// Outputs i..i+3 read x[2i-1 : 2i+9], which stays inside the 2*len(dst)
// inputs. With a = x[2i-1:2i+3] and b = x[2i+3:2i+7], the left taps
// x[2i-1+2k] are lanes (a0, a2, b0, b2) and the center taps x[2i+2k] are
// (a1, a3, b1, b3); the right taps come the same way from the loads one
// sample later. Each lane sums its three products in the scalar order.
//
//go:noinline
func pitchDownsample2AVX(dst, x0, x1 []float32) int {
	n := len(dst)
	x0 = x0[:2*n]
	q := broadcastF32x4Arch(0.25)
	h := broadcastF32x4Arch(0.5)
	decimate := func(w []float32) archsimd.Float32x4 {
		w = w[:10:10]
		a := archsimd.LoadFloat32x4Array((*[4]float32)(w[0:4]))
		b := archsimd.LoadFloat32x4Array((*[4]float32)(w[4:8]))
		ar := archsimd.LoadFloat32x4Array((*[4]float32)(w[2:6]))
		br := archsimd.LoadFloat32x4Array((*[4]float32)(w[6:10]))
		left := a.ConcatPermuteScalars(0, 2, 4, 6, b)
		center := a.ConcatPermuteScalars(1, 3, 5, 7, b)
		right := ar.ConcatPermuteScalars(0, 2, 4, 6, br)
		return q.Mul(left).Add(q.Mul(right)).Add(h.Mul(center))
	}
	i := 1
	if x1 == nil {
		for ; i+4 < n; i += 4 {
			decimate(x0[2*i-1:]).StoreArray((*[4]float32)(dst[i:]))
		}
		return i
	}
	x1 = x1[:2*n]
	for ; i+4 < n; i += 4 {
		v0 := decimate(x0[2*i-1:])
		v1 := decimate(x1[2*i-1:])
		v0.Add(v1).StoreArray((*[4]float32)(dst[i:]))
	}
	return i
}
