//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var celtFIR5UsesAVX = archsimd.X86.AVX()
var celtFIR5UsesAVX2 = archsimd.X86.AVX2() && (!celtFIR5UsesFMA || archsimd.X86.FMA())
var pitchDownsamplePerm0213U64x4 = [4]uint64{0, 2, 1, 3}

// celtFIR5F32 is libopus celt_fir5() applied in place to x. The filter has no
// feedback: every output is x[i] plus the five taps on the original inputs
// x[i-1] ... x[i-5]. The AVX2/AVX path computes eight or four outputs per
// vector in the selected libopus MAC order and walks from the end of x, so the
// inputs a vector reads are not yet overwritten.
func celtFIR5F32(x []float32, num [5]float32) {
	if celtFIR5UsesAVX2 && len(x) >= 13 {
		celtFIR5AVX2(x, num)
		return
	}
	if !celtFIR5UsesAVX || (len(x) < 9 && !celtFIR5UsesFMA) {
		celtFIR5Scalar(x, num)
		return
	}
	celtFIR5AVX(x, num)
}

//go:noinline
func celtFIR5AVX2(x []float32, num [5]float32) {
	n0 := archsimd.BroadcastFloat32x8(num[0])
	n1 := archsimd.BroadcastFloat32x8(num[1])
	n2 := archsimd.BroadcastFloat32x8(num[2])
	n3 := archsimd.BroadcastFloat32x8(num[3])
	n4 := archsimd.BroadcastFloat32x8(num[4])
	// Vector i covers outputs [i, i+8) and reads x[i-5 : i+8], so i >= 5.
	i := len(x) - 8
	for ; i >= 5; i -= 8 {
		w := x[i-5 : i+8 : i+8]
		s := archsimd.LoadFloat32x8Array((*[8]float32)(w[5:13]))
		s = celtFIR5Accumulate8(s, n0, archsimd.LoadFloat32x8Array((*[8]float32)(w[4:12])))
		s = celtFIR5Accumulate8(s, n1, archsimd.LoadFloat32x8Array((*[8]float32)(w[3:11])))
		s = celtFIR5Accumulate8(s, n2, archsimd.LoadFloat32x8Array((*[8]float32)(w[2:10])))
		s = celtFIR5Accumulate8(s, n3, archsimd.LoadFloat32x8Array((*[8]float32)(w[1:9])))
		s = celtFIR5Accumulate8(s, n4, archsimd.LoadFloat32x8Array((*[8]float32)(w[0:8])))
		s.StoreArray((*[8]float32)(w[5:13]))
	}
	archsimd.ClearAVXUpperBits()
	rem := i + 8
	if rem >= 9 {
		n04 := broadcastF32x4Arch(num[0])
		n14 := broadcastF32x4Arch(num[1])
		n24 := broadcastF32x4Arch(num[2])
		n34 := broadcastF32x4Arch(num[3])
		n44 := broadcastF32x4Arch(num[4])
		j := rem - 4
		for ; j >= 5; j -= 4 {
			w := x[j-5 : j+4 : j+4]
			s := archsimd.LoadFloat32x4Array((*[4]float32)(w[5:9]))
			s = celtFIR5Accumulate(s, n04, archsimd.LoadFloat32x4Array((*[4]float32)(w[4:8])))
			s = celtFIR5Accumulate(s, n14, archsimd.LoadFloat32x4Array((*[4]float32)(w[3:7])))
			s = celtFIR5Accumulate(s, n24, archsimd.LoadFloat32x4Array((*[4]float32)(w[2:6])))
			s = celtFIR5Accumulate(s, n34, archsimd.LoadFloat32x4Array((*[4]float32)(w[1:5])))
			s = celtFIR5Accumulate(s, n44, archsimd.LoadFloat32x4Array((*[4]float32)(w[0:4])))
			s.StoreArray((*[4]float32)(w[5:9]))
		}
		rem = j + 4
	}
	celtFIR5Head(x[:rem], num)
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
// [1, len(dst)); see pitchDownsample2Scalar. The AVX2/AVX path produces eight
// or four outputs per step and leaves the tail to the scalar loop.
func pitchDownsample2(dst, x0, x1 []float32) {
	start := 1
	if celtFIR5UsesAVX2 && len(dst) > 9 {
		start = pitchDownsample2AVX2(dst, x0, x1)
	} else if celtFIR5UsesAVX && len(dst) > 5 {
		start = pitchDownsample2AVX(dst, x0, x1)
	}
	pitchDownsample2Scalar(dst, x0, x1, start)
}

//go:noinline
func pitchDownsample2AVX2(dst, x0, x1 []float32) int {
	n := len(dst)
	x0 = x0[:2*n]
	perm0213 := archsimd.LoadUint64x4Array(&pitchDownsamplePerm0213U64x4)
	q := archsimd.BroadcastFloat32x8(0.25)
	h := archsimd.BroadcastFloat32x8(0.5)
	decimate8 := func(w []float32) archsimd.Float32x8 {
		w = w[:18:18]
		a := archsimd.LoadFloat32x8Array((*[8]float32)(w[0:8]))
		b := archsimd.LoadFloat32x8Array((*[8]float32)(w[8:16]))
		ar := archsimd.LoadFloat32x8Array((*[8]float32)(w[2:10]))
		br := archsimd.LoadFloat32x8Array((*[8]float32)(w[10:18]))
		left := a.ConcatPermuteScalarsGrouped(0, 2, 4, 6, b).AsUint64x4().Permute(perm0213).AsFloat32x8()
		center := a.ConcatPermuteScalarsGrouped(1, 3, 5, 7, b).AsUint64x4().Permute(perm0213).AsFloat32x8()
		right := ar.ConcatPermuteScalarsGrouped(0, 2, 4, 6, br).AsUint64x4().Permute(perm0213).AsFloat32x8()
		return q.Mul(left).Add(q.Mul(right)).Add(h.Mul(center))
	}
	i := 1
	if x1 == nil {
		for ; i+8 < n; i += 8 {
			decimate8(x0[2*i-1:]).StoreArray((*[8]float32)(dst[i:]))
		}
		archsimd.ClearAVXUpperBits()
		return i
	}
	x1 = x1[:2*n]
	for ; i+8 < n; i += 8 {
		v0 := decimate8(x0[2*i-1:])
		v1 := decimate8(x1[2*i-1:])
		v0.Add(v1).StoreArray((*[8]float32)(dst[i:]))
	}
	archsimd.ClearAVXUpperBits()
	return i
}

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
