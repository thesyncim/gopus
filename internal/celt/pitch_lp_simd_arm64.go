//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// celtFIR5F32 is libopus celt_fir5() applied in place to x. Four NEON lanes
// walk backwards from the end of x using fused MulAdd, matching the ARM64
// scalar contraction order bit-for-bit.
func celtFIR5F32(x []float32, num [5]float32) {
	if len(x) < 9 {
		celtFIR5Scalar(x, num)
		return
	}
	n0 := archsimd.BroadcastFloat32x4(num[0])
	n1 := archsimd.BroadcastFloat32x4(num[1])
	n2 := archsimd.BroadcastFloat32x4(num[2])
	n3 := archsimd.BroadcastFloat32x4(num[3])
	n4 := archsimd.BroadcastFloat32x4(num[4])
	i := len(x) - 4
	for ; i >= 5; i -= 4 {
		w := x[i-5 : i+4 : i+4]
		s := archsimd.LoadFloat32x4Array((*[4]float32)(w[5:9]))
		s = n0.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(w[4:8])), s)
		s = n1.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(w[3:7])), s)
		s = n2.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(w[2:6])), s)
		s = n3.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(w[1:5])), s)
		s = n4.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(w[0:4])), s)
		s.StoreArray((*[4]float32)(w[5:9]))
	}
	celtFIR5Scalar(x[:i+4], num)
}

// pitchDownsample2 is the factor-2 pitch_downsample() decimation of outputs
// [1, len(dst)). Four NEON lanes per step deinterleave left/center/right taps
// with ConcatEven/ConcatOdd (UZP1/UZP2) and leave the tail to the scalar loop.
func pitchDownsample2(dst, x0, x1 []float32) {
	n := len(dst)
	if n <= 5 {
		pitchDownsample2Scalar(dst, x0, x1, 1)
		return
	}
	x0 = x0[:2*n]
	q := archsimd.BroadcastFloat32x4(0.25)
	h := archsimd.BroadcastFloat32x4(0.5)
	decimate := func(w []float32) archsimd.Float32x4 {
		w = w[:10:10]
		a := archsimd.LoadFloat32x4Array((*[4]float32)(w[0:4])).ToBits()
		b := archsimd.LoadFloat32x4Array((*[4]float32)(w[4:8])).ToBits()
		ar := archsimd.LoadFloat32x4Array((*[4]float32)(w[2:6])).ToBits()
		br := archsimd.LoadFloat32x4Array((*[4]float32)(w[6:10])).ToBits()
		left := a.ConcatEven(b).BitsToFloat32()
		center := a.ConcatOdd(b).BitsToFloat32()
		right := ar.ConcatEven(br).BitsToFloat32()
		return h.MulAdd(center, q.MulAdd(right, q.Mul(left)))
	}
	i := 1
	if x1 == nil {
		for ; i+4 < n; i += 4 {
			decimate(x0[2*i-1:]).StoreArray((*[4]float32)(dst[i:]))
		}
	} else {
		x1 = x1[:2*n]
		for ; i+4 < n; i += 4 {
			v0 := decimate(x0[2*i-1:])
			v1 := decimate(x1[2*i-1:])
			v0.Add(v1).StoreArray((*[4]float32)(dst[i:]))
		}
	}
	pitchDownsample2Scalar(dst, x0, x1, i)
}
