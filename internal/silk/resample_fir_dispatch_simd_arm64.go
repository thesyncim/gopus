//go:build arm64 && goexperiment.simd && !nosimd

package silk

func firInterpol21846CoreDispatch(dst []int16, buf []int16, nOut int) {
	firInterpol21846CoreSIMD(dst, buf, nOut)
}

func firInterpol32768CoreDispatch(dst []int16, buf []int16, nOut int) {
	firInterpol32768CoreSIMD(dst, buf, nOut)
}
