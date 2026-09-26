//go:build amd64 && goexperiment.simd && !nosimd

package celt

import "simd/archsimd"

func libopusFloatPitchXCorrUsesAVX2FMA() bool {
	return archsimd.X86.AVX2() && archsimd.X86.FMA()
}
