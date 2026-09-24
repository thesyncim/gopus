//go:build arm64 && goexperiment.simd && !nosimd

package silk

import (
	"simd/archsimd"
	"unsafe"
)

var firInterpol21846SIMDCoefs = [3][8]int16{
	{189, -600, 617, 30567, 2996, -1375, 425, -46},
	{-48, 758, -3956, 23973, 15143, -3957, 967, -107},
	{-103, 896, -3487, 11950, 26341, -3350, 529, -4},
}

func firInterpol21846CoreSIMD(dst []int16, buf []int16, nOut int) {
	if nOut <= 0 {
		return
	}
	_ = dst[nOut-1]
	_ = buf[(nOut-1)/3+7]

	coef0 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[0])
	coef4 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[1])
	coef8 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[2])
	coef0Hi := coef0.HiToLo()
	coef4Hi := coef4.HiToLo()
	coef8Hi := coef8.HiToLo()
	bufPtr := unsafe.Pointer(unsafe.SliceData(buf))
	dstPtr := unsafe.Pointer(unsafe.SliceData(dst))
	groups := nOut / 3
	for i := 0; i < groups; i++ {
		x := archsimd.LoadInt16x8Array((*[8]int16)(unsafe.Add(bufPtr, i*2)))
		xHi := x.HiToLo()
		res0 := firInterpol21846Dot(x, xHi, coef0, coef0Hi)
		res4 := firInterpol21846Dot(x, xHi, coef4, coef4Hi)
		res8 := firInterpol21846Dot(x, xHi, coef8, coef8Hi)
		outIdx := i * 3
		*(*int16)(unsafe.Add(dstPtr, outIdx*2)) = sat16RShiftRound15(res0)
		*(*int16)(unsafe.Add(dstPtr, (outIdx+1)*2)) = sat16RShiftRound15(res4)
		*(*int16)(unsafe.Add(dstPtr, (outIdx+2)*2)) = sat16RShiftRound15(res8)
	}
	if tail := nOut - groups*3; tail != 0 {
		firInterpol21846CoreGo(dst[groups*3:], buf[groups:], tail)
	}
}

func firInterpol21846Dot(xLo, xHi, coefLo, coefHi archsimd.Int16x8) int32 {
	sum := xLo.MulWidenLo(coefLo).Add(xHi.MulWidenLo(coefHi))
	return sum.ReduceSum()
}
