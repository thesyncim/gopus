//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// absSumQuad returns absSumSerial(a), absSumSerial(b), absSumSerial(c) and
// absSumSerial(d); b, c and d hold at least len(a) elements. Each 4-element
// block is transposed across four NEON lanes so every lane performs its slice's
// exact serial addition order.
func absSumQuad(a, b, c, d []float32) (float32, float32, float32, float32) {
	n := len(a)
	b, c, d = b[:n], c[:n], d[:n]
	blocks := n &^ 3
	var s0, s1, s2, s3 float32
	if blocks > 0 {
		var acc archsimd.Float32x4
		for j := 0; j+4 <= blocks; j += 4 {
			va := archsimd.LoadFloat32x4Array((*[4]float32)(a[j:])).Abs().ToBits()
			vb := archsimd.LoadFloat32x4Array((*[4]float32)(b[j:])).Abs().ToBits()
			vc := archsimd.LoadFloat32x4Array((*[4]float32)(c[j:])).Abs().ToBits()
			vd := archsimd.LoadFloat32x4Array((*[4]float32)(d[j:])).Abs().ToBits()
			ab01 := va.InterleaveLo(vb).ReshapeToUint64s() // [a0 b0, a1 b1]
			cd01 := vc.InterleaveLo(vd).ReshapeToUint64s() // [c0 d0, c1 d1]
			ab23 := va.InterleaveHi(vb).ReshapeToUint64s() // [a2 b2, a3 b3]
			cd23 := vc.InterleaveHi(vd).ReshapeToUint64s() // [c2 d2, c3 d3]
			acc = acc.Add(ab01.InterleaveLo(cd01).ReshapeToUint32s().BitsToFloat32())
			acc = acc.Add(ab01.InterleaveHi(cd01).ReshapeToUint32s().BitsToFloat32())
			acc = acc.Add(ab23.InterleaveLo(cd23).ReshapeToUint32s().BitsToFloat32())
			acc = acc.Add(ab23.InterleaveHi(cd23).ReshapeToUint32s().BitsToFloat32())
		}
		s0, s1, s2, s3 = acc.GetElem(0), acc.GetElem(1), acc.GetElem(2), acc.GetElem(3)
	}
	for j := blocks; j < n; j++ {
		s0 += absF32(a[j])
		s1 += absF32(b[j])
		s2 += absF32(c[j])
		s3 += absF32(d[j])
	}
	return s0, s1, s2, s3
}

// absSumPair is absSumQuad for two slices: the two accumulators occupy lanes 0
// and 1, and each block's elements are added in order.
func absSumPair(a, b []float32) (float32, float32) {
	n := len(a)
	b = b[:n]
	blocks := n &^ 3
	var s0, s1 float32
	if blocks > 0 {
		var acc archsimd.Float32x4
		for j := 0; j+4 <= blocks; j += 4 {
			va := archsimd.LoadFloat32x4Array((*[4]float32)(a[j:])).Abs().ToBits()
			vb := archsimd.LoadFloat32x4Array((*[4]float32)(b[j:])).Abs().ToBits()
			ab01 := va.InterleaveLo(vb).ReshapeToUint64s() // [a0 b0, a1 b1]
			ab23 := va.InterleaveHi(vb).ReshapeToUint64s() // [a2 b2, a3 b3]
			acc = acc.Add(ab01.ReshapeToUint32s().BitsToFloat32())
			acc = acc.Add(ab01.InterleaveHi(ab01).ReshapeToUint32s().BitsToFloat32())
			acc = acc.Add(ab23.ReshapeToUint32s().BitsToFloat32())
			acc = acc.Add(ab23.InterleaveHi(ab23).ReshapeToUint32s().BitsToFloat32())
		}
		s0, s1 = acc.GetElem(0), acc.GetElem(1)
	}
	for j := blocks; j < n; j++ {
		s0 += absF32(a[j])
		s1 += absF32(b[j])
	}
	return s0, s1
}

// absSumFive is absSumQuad for the first four slices and absSumSerial for e.
func absSumFive(a, b, c, d, e []float32) (float32, float32, float32, float32, float32) {
	s0, s1, s2, s3 := absSumQuad(a, b, c, d)
	return s0, s1, s2, s3, absSumSerial(e[:len(a)])
}
