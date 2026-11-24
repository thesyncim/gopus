//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import "simd/archsimd"

// celtPLCXcorrKernel4Float32SSE matches celt/celt_lpc.c:celt_fir_c calling
// celt/x86/pitch_sse.c:xcorr_kernel_sse in the pinned x86-v3 SIMD build. GCC
// contracts each packed multiply/add into FMA, and keeps the same even/odd
// accumulator order as the existing scalar-shaped SSE-order helper.
func celtPLCXcorrKernel4Float32SSE(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	x = x[:length]
	y = y[:length+3]
	accEven := archsimd.LoadFloat32x4Array(sum)
	var accOdd archsimd.Float32x4

	j := 0
	for ; j+4 <= length; j += 4 {
		yj := y[j : j+7 : j+7]
		accEven = broadcastF32x4Arch(x[j]).MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(yj[0:4])), accEven)
		accOdd = broadcastF32x4Arch(x[j+1]).MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(yj[1:5])), accOdd)
		accEven = broadcastF32x4Arch(x[j+2]).MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(yj[2:6])), accEven)
		accOdd = broadcastF32x4Arch(x[j+3]).MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(yj[3:7])), accOdd)
	}
	if j < length {
		accEven = broadcastF32x4Arch(x[j]).MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(y[j:])), accEven)
		j++
		if j < length {
			accOdd = broadcastF32x4Arch(x[j]).MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(y[j:])), accOdd)
			j++
			if j < length {
				accEven = broadcastF32x4Arch(x[j]).MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(y[j:])), accEven)
			}
		}
	}
	accEven.Add(accOdd).StoreArray(sum)
}
