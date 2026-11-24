//go:build amd64 && goexperiment.simd && !nosimd && !purego

package gopus

import "simd/archsimd"

var smoothFadeUsesAVX = archsimd.X86.AVX()

// smoothFade48kSIMD vectorizes 48 kHz mono and stereo smoothFade 4 frames at a
// time on AMD64 AVX using separately rounded products matching libopus -O3.
func smoothFade48kSIMD(in1, in2, out, win []float32, overlap, channels int) bool {
	if !smoothFadeUsesAVX {
		return false
	}
	smoothFade48kAVX(in1, in2, out, win, overlap, channels)
	return true
}

//go:noinline
func smoothFade48kAVX(in1, in2, out, win []float32, overlap, channels int) {
	one := archsimd.BroadcastFloat32x4(1)
	if channels == 1 {
		i := 0
		for ; i+4 <= overlap; i += 4 {
			w := archsimd.LoadFloat32x4Array((*[4]float32)(win[i:]))
			w = w.Mul(w)
			oneMinusW := one.Sub(w)
			i1 := archsimd.LoadFloat32x4Array((*[4]float32)(in1[i:]))
			i2 := archsimd.LoadFloat32x4Array((*[4]float32)(in2[i:]))
			w.Mul(i2).Add(oneMinusW.Mul(i1)).StoreArray((*[4]float32)(out[i:]))
		}
		for ; i < overlap; i++ {
			w := float32(win[i] * win[i])
			oneMinusW := float32(1 - w)
			out[i] = float32(w*in2[i]) + float32(oneMinusW*in1[i])
		}
		return
	}
	i := 0
	for ; i+4 <= overlap; i += 4 {
		w := archsimd.LoadFloat32x4Array((*[4]float32)(win[i:]))
		w = w.Mul(w)
		oneMinusW := one.Sub(w)
		wLo := w.ConcatPermuteScalars(0, 0, 1, 1, w)
		wHi := w.ConcatPermuteScalars(2, 2, 3, 3, w)
		omLo := oneMinusW.ConcatPermuteScalars(0, 0, 1, 1, oneMinusW)
		omHi := oneMinusW.ConcatPermuteScalars(2, 2, 3, 3, oneMinusW)
		idx := 2 * i
		i1Lo := archsimd.LoadFloat32x4Array((*[4]float32)(in1[idx:]))
		i2Lo := archsimd.LoadFloat32x4Array((*[4]float32)(in2[idx:]))
		wLo.Mul(i2Lo).Add(omLo.Mul(i1Lo)).StoreArray((*[4]float32)(out[idx:]))
		i1Hi := archsimd.LoadFloat32x4Array((*[4]float32)(in1[idx+4:]))
		i2Hi := archsimd.LoadFloat32x4Array((*[4]float32)(in2[idx+4:]))
		wHi.Mul(i2Hi).Add(omHi.Mul(i1Hi)).StoreArray((*[4]float32)(out[idx+4:]))
	}
	for ; i < overlap; i++ {
		w := float32(win[i] * win[i])
		oneMinusW := float32(1 - w)
		idx := 2 * i
		out[idx] = float32(w*in2[idx]) + float32(oneMinusW*in1[idx])
		out[idx+1] = float32(w*in2[idx+1]) + float32(oneMinusW*in1[idx+1])
	}
}
