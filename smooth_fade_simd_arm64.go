//go:build arm64 && goexperiment.simd && !nosimd && !purego

package gopus

import "simd/archsimd"

// smoothFade48kSIMD vectorizes 48 kHz mono and stereo smoothFade 4 frames at a
// time on ARM64 NEON, matching the scalar fused MulAdd order bit-for-bit.
func smoothFade48kSIMD(in1, in2, out, win []float32, overlap, channels int) bool {
	one := archsimd.BroadcastFloat32x4(1)
	if channels == 1 {
		i := 0
		for ; i+4 <= overlap; i += 4 {
			w := archsimd.LoadFloat32x4Array((*[4]float32)(win[i:]))
			w = w.Mul(w)
			oneMinusW := one.Sub(w)
			i1 := archsimd.LoadFloat32x4Array((*[4]float32)(in1[i:]))
			i2 := archsimd.LoadFloat32x4Array((*[4]float32)(in2[i:]))
			w.MulAdd(i2, oneMinusW.Mul(i1)).StoreArray((*[4]float32)(out[i:]))
		}
		for ; i < overlap; i++ {
			w := float32(win[i] * win[i])
			oneMinusW := float32(1 - w)
			out[i] = w*in2[i] + float32(oneMinusW*in1[i])
		}
		return true
	}
	i := 0
	for ; i+4 <= overlap; i += 4 {
		w := archsimd.LoadFloat32x4Array((*[4]float32)(win[i:]))
		w = w.Mul(w)
		oneMinusW := one.Sub(w)
		wb := w.ToBits()
		omb := oneMinusW.ToBits()
		wLo := wb.InterleaveLo(wb).BitsToFloat32()
		wHi := wb.InterleaveHi(wb).BitsToFloat32()
		omLo := omb.InterleaveLo(omb).BitsToFloat32()
		omHi := omb.InterleaveHi(omb).BitsToFloat32()
		idx := 2 * i
		i1Lo := archsimd.LoadFloat32x4Array((*[4]float32)(in1[idx:]))
		i2Lo := archsimd.LoadFloat32x4Array((*[4]float32)(in2[idx:]))
		wLo.MulAdd(i2Lo, omLo.Mul(i1Lo)).StoreArray((*[4]float32)(out[idx:]))
		i1Hi := archsimd.LoadFloat32x4Array((*[4]float32)(in1[idx+4:]))
		i2Hi := archsimd.LoadFloat32x4Array((*[4]float32)(in2[idx+4:]))
		wHi.MulAdd(i2Hi, omHi.Mul(i1Hi)).StoreArray((*[4]float32)(out[idx+4:]))
	}
	for ; i < overlap; i++ {
		w := float32(win[i] * win[i])
		oneMinusW := float32(1 - w)
		idx := 2 * i
		out[idx] = w*in2[idx] + float32(oneMinusW*in1[idx])
		out[idx+1] = w*in2[idx+1] + float32(oneMinusW*in1[idx+1])
	}
	return true
}
