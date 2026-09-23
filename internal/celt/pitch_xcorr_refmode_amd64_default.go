//go:build amd64 && !goexperiment.simd && !nosimd

package celt

func libopusFloatPitchXCorrUsesAVX2FMA() bool {
	return false
}
