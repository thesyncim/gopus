//go:build arm64 && goexperiment.simd && !nosimd

package celt

func expRotationHostSupportsSIMD() bool {
	return true
}
