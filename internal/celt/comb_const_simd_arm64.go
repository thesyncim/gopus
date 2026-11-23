//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// combUsesNeon selects the archsimd constant-gain comb filter on the arm64
// experiment build.
const combUsesNeon = true

// combFilterConstNeon applies the 5-tap symmetric comb filter
//
//	dst[j] += g10*delay[j+2] + g11*(delay[j+1]+delay[j+3]) + g12*(delay[j]+delay[j+4])
//
// four lanes at a time. The two tap sums round as plain FADDs and the three
// accumulates are fused FMLAs (MulAdd), matching the scalar combFilterConstValue
// bit-for-bit. blocks counts four-output vectors.
func combFilterConstNeonBlock(base, d0, d1, g10v, g11v, g12v archsimd.Float32x4) archsimd.Float32x4 {
	lo := d0.ToBits().ReshapeToUint8s()
	hi := d1.ToBits().ReshapeToUint8s()
	center := hi.ConcatShiftBytesRight(lo, 8).ReshapeToUint32s().BitsToFloat32()
	plus1 := hi.ConcatShiftBytesRight(lo, 4).ReshapeToUint32s().BitsToFloat32()
	plus3 := hi.ConcatShiftBytesRight(lo, 12).ReshapeToUint32s().BitsToFloat32()
	s1 := plus3.Add(plus1)
	s2 := d1.Add(d0)
	sum := center.MulAdd(g10v, base)
	sum = s1.MulAdd(g11v, sum)
	return s2.MulAdd(g12v, sum)
}

func combFilterConstNeon(dst, delay []float32, g10, g11, g12 float32, blocks int) {
	if blocks <= 0 {
		return
	}
	n := blocks * 4
	dst = dst[:n]
	delay = delay[:n+4]
	g10v := archsimd.BroadcastFloat32x4(g10)
	g11v := archsimd.BroadcastFloat32x4(g11)
	g12v := archsimd.BroadcastFloat32x4(g12)
	j := 0
	for ; j+8 <= n; j += 8 {
		d0 := archsimd.LoadFloat32x4Array((*[4]float32)(delay[j:]))
		d1 := archsimd.LoadFloat32x4Array((*[4]float32)(delay[j+4:]))
		d2 := archsimd.LoadFloat32x4Array((*[4]float32)(delay[j+8:]))
		base0 := archsimd.LoadFloat32x4Array((*[4]float32)(dst[j:]))
		combFilterConstNeonBlock(base0, d0, d1, g10v, g11v, g12v).StoreArray((*[4]float32)(dst[j:]))
		base1 := archsimd.LoadFloat32x4Array((*[4]float32)(dst[j+4:]))
		combFilterConstNeonBlock(base1, d1, d2, g10v, g11v, g12v).StoreArray((*[4]float32)(dst[j+4:]))
	}
	if j+4 <= n {
		base := archsimd.LoadFloat32x4Array((*[4]float32)(dst[j:]))
		d0 := archsimd.LoadFloat32x4Array((*[4]float32)(delay[j:]))
		d1 := archsimd.LoadFloat32x4Array((*[4]float32)(delay[j+4:]))
		combFilterConstNeonBlock(base, d0, d1, g10v, g11v, g12v).StoreArray((*[4]float32)(dst[j:]))
	}
}
