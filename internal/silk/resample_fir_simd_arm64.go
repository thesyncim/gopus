//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

var firInterpol21846SIMDCoefs = [3][8]int16{
	{189, -600, 617, 30567, 2996, -1375, 425, -46},
	{-48, 758, -3956, 23973, 15143, -3957, 967, -107},
	{-103, 896, -3487, 11950, 26341, -3350, 529, -4},
}

var firInterpol32768SIMDCoefs = [2][8]int16{
	{189, -600, 617, 30567, 2996, -1375, 425, -46},
	{-99, 972, -4222, 18278, 21254, -4235, 905, -80},
}

func firInterpol21846CoreSIMD(dst []int16, buf []int16, nOut int) {
	if nOut <= 0 {
		return
	}
	dst = dst[:nOut]
	groups := nOut / 3
	if groups > 0 {
		buf = buf[:groups+7]
		coef0 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[0])
		coef4 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[1])
		coef8 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[2])
		coef0Hi := coef0.HiToLo()
		coef4Hi := coef4.HiToLo()
		coef8Hi := coef8.HiToLo()
		out := dst[:groups*3]
		for i := range groups {
			x := archsimd.LoadInt16x8Array((*[8]int16)(buf[i:]))
			xHi := x.HiToLo()
			d := (*[3]int16)(out[i*3:])
			d[0] = sat16RShiftRound15(firInterpolDotSIMD(x, xHi, coef0, coef0Hi))
			d[1] = sat16RShiftRound15(firInterpolDotSIMD(x, xHi, coef4, coef4Hi))
			d[2] = sat16RShiftRound15(firInterpolDotSIMD(x, xHi, coef8, coef8Hi))
		}
	}
	if tail := nOut - groups*3; tail != 0 {
		firInterpol21846CoreGo(dst[groups*3:], buf[groups:], tail)
	}
}

func firInterpolDotSIMD(xLo, xHi, coefLo, coefHi archsimd.Int16x8) int32 {
	sum := xLo.MulWidenLo(coefLo).Add(xHi.MulWidenLo(coefHi))
	return sum.ReduceSum()
}

func firInterpol32768CoreSIMD(dst []int16, buf []int16, nOut int) {
	if nOut <= 0 {
		return
	}
	dst = dst[:nOut]
	groups := nOut / 2
	if groups > 0 {
		buf = buf[:groups+7]
		coef0 := archsimd.LoadInt16x8Array(&firInterpol32768SIMDCoefs[0])
		coef6 := archsimd.LoadInt16x8Array(&firInterpol32768SIMDCoefs[1])
		coef0Hi := coef0.HiToLo()
		coef6Hi := coef6.HiToLo()
		out := dst[:groups*2]
		for i := range groups {
			x := archsimd.LoadInt16x8Array((*[8]int16)(buf[i:]))
			xHi := x.HiToLo()
			d := (*[2]int16)(out[i*2:])
			d[0] = sat16RShiftRound15(firInterpolDotSIMD(x, xHi, coef0, coef0Hi))
			d[1] = sat16RShiftRound15(firInterpolDotSIMD(x, xHi, coef6, coef6Hi))
		}
	}
	if tail := nOut - groups*2; tail != 0 {
		firInterpol32768CoreGo(dst[groups*2:], buf[groups:], tail)
	}
}

func firInterpol43691CoreSIMD(dst []int16, buf []int16, nOut int) {
	if nOut <= 0 {
		return
	}
	dst = dst[:nOut]
	groups := nOut / 3
	if groups > 0 {
		buf = buf[:groups*2+7]
		coef0 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[0])
		coef4 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[1])
		coef8 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[2])
		coef0Hi := coef0.HiToLo()
		coef4Hi := coef4.HiToLo()
		coef8Hi := coef8.HiToLo()
		out := dst[:groups*3]
		for i := range groups {
			in := buf[i*2:]
			x0 := archsimd.LoadInt16x8Array((*[8]int16)(in))
			x0Hi := x0.HiToLo()
			x1 := archsimd.LoadInt16x8Array((*[8]int16)(in[1:]))
			x1Hi := x1.HiToLo()
			d := (*[3]int16)(out[i*3:])
			d[0] = sat16RShiftRound15(firInterpolDotSIMD(x0, x0Hi, coef0, coef0Hi))
			d[1] = sat16RShiftRound15(firInterpolDotSIMD(x0, x0Hi, coef8, coef8Hi))
			d[2] = sat16RShiftRound15(firInterpolDotSIMD(x1, x1Hi, coef4, coef4Hi))
		}
	}
	if tail := nOut - groups*3; tail != 0 {
		firInterpol43691CoreGo(dst[groups*3:], buf[groups*2:], tail)
	}
}
