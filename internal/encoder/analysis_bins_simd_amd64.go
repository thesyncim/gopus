//go:build amd64 && goexperiment.simd && !nosimd && !purego

package encoder

import "simd/archsimd"

// Go 1.27 lowers Float32x4.Abs and Neg to AVX2 instructions even though
// their API lists AVX (golang/go#81405). Keep this kernel behind AVX2.
var analysisBinsUseAVX2 = archsimd.X86.AVX2() && (!analysisAvgModFMAEnabled || archsimd.X86.FMA())

func (s *TonalityAnalysisState) analysisBinsSIMD(out *[480]complex64, tonality, tonality2, noisiness []float32) int {
	if !analysisBinsUseAVX2 {
		return 1
	}
	return s.analysisBinsAVX2(out, tonality, tonality2, noisiness)
}

// analysisBinsAVX2 runs tonality_analysis's per-bin phase loop for bins
// 1..236, four bins per step, and returns the first bin left to the scalar
// loop. Every lane preserves the scalar operand order. The call boundary
// keeps Go 1.27 from hoisting SIMD instructions above the CPU check.
//
//go:noinline
func (s *TonalityAnalysisState) analysisBinsAVX2(out *[480]complex64, tonality, tonality2, noisiness []float32) int {
	tonality = tonality[:240]
	tonality2 = tonality2[:240]
	noisiness = noisiness[:240]
	of := complex64AsFloat32(out[:])
	scale := archsimd.BroadcastFloat32x4(analysisAtanScale)
	quarter := archsimd.BroadcastFloat32x4(0.25)
	two := archsimd.BroadcastFloat32x4(2)
	one := archsimd.BroadcastFloat32x4(1)
	k := archsimd.BroadcastFloat32x4(40.0 * 16.0 * analysisPi4)
	c015 := archsimd.BroadcastFloat32x4(0.015)
	i := 1
	for ; i+4 <= 237; i += 4 {
		lo := archsimd.LoadFloat32x4Array((*[4]float32)(of[2*i:]))
		hi := archsimd.LoadFloat32x4Array((*[4]float32)(of[2*i+4:]))
		re := lo.ConcatPermuteScalars(0, 2, 4, 6, hi)
		im := lo.ConcatPermuteScalars(1, 3, 5, 7, hi)
		// Mirror bins 480-i-k for lanes k = 0..3.
		mlo := archsimd.LoadFloat32x4Array((*[4]float32)(of[2*(477-i):]))
		mhi := archsimd.LoadFloat32x4Array((*[4]float32)(of[2*(479-i):]))
		reM := mhi.ConcatPermuteScalars(2, 0, 6, 4, mlo)
		imM := mhi.ConcatPermuteScalars(3, 1, 7, 5, mlo)

		if analysisAvgModFMAEnabled {
			imM.MulAdd(imM, im.MulAdd(im, re.MulAdd(re, reM.Mul(reM)))).StoreArray((*[4]float32)(s.scratchBinE[i : i+4]))
		} else {
			reM.Mul(reM).Add(re.Mul(re)).Add(im.Mul(im)).Add(imM.Mul(imM)).StoreArray((*[4]float32)(s.scratchBinE[i : i+4]))
		}

		x1r := re.Add(reM)
		x1i := im.Sub(imM)
		x2r := im.Add(imM)
		x2i := reM.Sub(re)

		angle := scale.Mul(analysisAtan2x4(x1i, x1r))
		dAngle := angle.Sub(archsimd.LoadFloat32x4Array((*[4]float32)(s.Angle[i:])))
		d2Angle := dAngle.Sub(archsimd.LoadFloat32x4Array((*[4]float32)(s.DAngle[i:])))

		angle2 := scale.Mul(analysisAtan2x4(x2i, x2r))
		dAngle2 := angle2.Sub(angle)
		d2Angle2 := dAngle2.Sub(dAngle)

		mod1 := d2Angle.Sub(analysisFloat2Intx4(d2Angle))
		mod2 := d2Angle2.Sub(analysisFloat2Intx4(d2Angle2))
		mod1.Abs().Add(mod2.Abs()).StoreArray((*[4]float32)(noisiness[i:]))
		mod1 = mod1.Mul(mod1)
		mod2 = mod2.Mul(mod2)
		mod2 = mod2.Mul(mod2)
		var avgMod archsimd.Float32x4
		d2Old := archsimd.LoadFloat32x4Array((*[4]float32)(s.D2Angle[i:]))
		if analysisAvgModFMAEnabled {
			// The matched amd64.v3 C compiler fuses the final mod1 square
			// into the old d2 history term at analysis.c:599.
			avgAccum := mod1.MulAdd(mod1, d2Old)
			avgMod = quarter.Mul(avgAccum.Add(two.Mul(mod2)))
		} else {
			mod1 = mod1.Mul(mod1)
			avgMod = quarter.Mul(d2Old.Add(mod1).Add(two.Mul(mod2)))
		}
		one.Div(analysisToneDenominatorSIMD(k, avgMod, one)).Sub(c015).StoreArray((*[4]float32)(tonality[i:]))
		one.Div(analysisToneDenominatorSIMD(k, mod2, one)).Sub(c015).StoreArray((*[4]float32)(tonality2[i:]))

		angle2.StoreArray((*[4]float32)(s.Angle[i:]))
		dAngle2.StoreArray((*[4]float32)(s.DAngle[i:]))
		mod2.StoreArray((*[4]float32)(s.D2Angle[i:]))
		if analysisPerBinTraceEnabled && analysisPerBinTraceHook != nil {
			var avgModValues [4]float32
			avgMod.StoreArray(&avgModValues)
			for lane := range 4 {
				bin := i + lane
				analysisPerBinTraceHook(analysisPerBinTraceSnapshot{
					Bin:       int32(bin),
					AvgMod:    avgModValues[lane],
					Tonality:  tonality[bin],
					Tonality2: tonality2[bin],
					Noisiness: noisiness[bin],
				})
			}
		}
	}
	return i
}

// analysisAtan2x4 is analysisAtan2 on four lanes.
func analysisAtan2x4(y, x archsimd.Float32x4) archsimd.Float32x4 {
	x2 := x.Mul(x)
	y2 := y.Mul(y)
	xy := x.Mul(y)
	zero := archsimd.Float32x4{}
	swap := x2.Less(y2)
	p := y2.IfElse(swap, x2)
	q := x2.IfElse(swap, y2)
	num := xy.Neg().IfElse(swap, xy).Mul(analysisAtan2PolyTermSIMD(p, q, archsimd.BroadcastFloat32x4(analysisAtanCA)))
	den := analysisAtan2PolyTermSIMD(p, q, archsimd.BroadcastFloat32x4(analysisAtanCB)).Mul(
		analysisAtan2PolyTermSIMD(p, q, archsimd.BroadcastFloat32x4(analysisAtanCC)))
	cE := archsimd.BroadcastFloat32x4(analysisAtanCE)
	negCE := archsimd.BroadcastFloat32x4(-analysisAtanCE)
	s1 := negCE.IfElse(y.Less(zero), cE)
	s2 := zero.IfElse(swap, negCE.IfElse(xy.Less(zero), cE))
	atan := num.Div(den).Add(s1).Sub(s2)
	return zero.IfElse(x2.Add(y2).Less(archsimd.BroadcastFloat32x4(1e-18)), atan)
}

// analysisFloat2Intx4 is float(float2int(x)) on four lanes: round to nearest
// even, with the out-of-range INT_MIN of cvtss2si.
func analysisFloat2Intx4(x archsimd.Float32x4) archsimd.Float32x4 {
	return x.Round().ConvertToInt32().ConvertToFloat32()
}

func smoothAnalysisTonality(tonality, tonality2 []float32) {
	tonality = tonality[:240:240]
	tonality2 = tonality2[:240:240]
	for i := 2; i < 239; i++ {
		tt := minf(tonality2[i], maxf(tonality2[i-1], tonality2[i+1]))
		tonality[i] = 0.9 * maxf(tonality[i], tt-0.1)
	}
}
