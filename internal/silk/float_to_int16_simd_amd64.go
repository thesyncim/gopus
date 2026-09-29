//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

var floatToInt16UsesAVX = archsimd.X86.AVX()

// floatToInt16Scaled converts eight products per step: VROUNDPS rounds to
// nearest with ties to even, VCVTTPS2DQ then converts the integral values (and
// yields the x86 integer-indefinite value for NaN and int32 overflow, as the
// scalar cvtss2si path does), and VPACKSSDW saturates to int16.
func floatToInt16Scaled(out []int16, in []float32, scale float32, n int) {
	out = out[:n]
	in = in[:n]
	i := 0
	if floatToInt16UsesAVX {
		s := archsimd.BroadcastFloat32x8(scale)
		for ; i+8 <= n; i += 8 {
			v := archsimd.LoadFloat32x8Array((*[8]float32)(in[i : i+8])).Mul(s).Round().ConvertToInt32()
			v.GetLo().SaturateToInt16Concat(v.GetHi()).StoreArray((*[8]int16)(out[i : i+8]))
		}
		archsimd.ClearAVXUpperBits()
	}
	floatToInt16ScaledScalar(out[i:], in[i:], scale)
}
