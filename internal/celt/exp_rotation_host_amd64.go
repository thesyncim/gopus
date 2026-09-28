//go:build amd64 && goexperiment.simd && !nosimd

package celt

import "simd/archsimd"

func expRotationHostSupportsSIMD() bool {
	return archsimd.X86.AVX()
}
