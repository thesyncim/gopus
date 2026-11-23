//go:build arm64 && goexperiment.simd && !nosimd && !purego

package encoder

import "simd/archsimd"

// analysisBinsSIMD runs tonality_analysis's per-bin phase loop for bins
// 1..236 four lanes at a time on ARM64 NEON, matching analysisBinsScalar
// bit-for-bit in every output and in the phase history.
func (s *TonalityAnalysisState) analysisBinsSIMD(out *[480]complex64, tonality, tonality2, noisiness []float32) int {
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
		lo := archsimd.LoadFloat32x4Array((*[4]float32)(of[2*i:])).ToBits()
		hi := archsimd.LoadFloat32x4Array((*[4]float32)(of[2*i+4:])).ToBits()
		re := lo.ConcatEven(hi).BitsToFloat32()
		im := lo.ConcatOdd(hi).BitsToFloat32()
		// Mirror bins 480-i-k for lanes k = 0..3.
		mlo := archsimd.LoadFloat32x4Array((*[4]float32)(of[2*(477-i):])).ToBits()
		mhi := archsimd.LoadFloat32x4Array((*[4]float32)(of[2*(479-i):])).ToBits()
		reM := reverse4F32(mlo.ConcatEven(mhi).BitsToFloat32())
		imM := reverse4F32(mlo.ConcatOdd(mhi).BitsToFloat32())

		x1r := re.Add(reM)
		x1i := im.Sub(imM)
		x2r := im.Add(imM)
		x2i := reM.Sub(re)

		angle := scale.Mul(analysisAtan2x4NEON(x1i, x1r))
		dAngle := angle.Sub(archsimd.LoadFloat32x4Array((*[4]float32)(s.Angle[i:])))
		d2Angle := dAngle.Sub(archsimd.LoadFloat32x4Array((*[4]float32)(s.DAngle[i:])))

		angle2 := scale.Mul(analysisAtan2x4NEON(x2i, x2r))
		dAngle2 := angle2.Sub(angle)
		d2Angle2 := dAngle2.Sub(dAngle)

		mod1 := d2Angle.Sub(analysisFloat2Intx4NEON(d2Angle))
		mod2 := d2Angle2.Sub(analysisFloat2Intx4NEON(d2Angle2))
		mod1.Abs().Add(mod2.Abs()).StoreArray((*[4]float32)(noisiness[i:]))
		mod1 = mod1.Mul(mod1)
		mod2 = mod2.Mul(mod2)
		mod2 = mod2.Mul(mod2)
		mod1 = mod1.Mul(mod1)
		d2Old := archsimd.LoadFloat32x4Array((*[4]float32)(s.D2Angle[i:]))
		avgMod := quarter.Mul(two.MulAdd(mod2, d2Old.Add(mod1)))
		one.Div(k.MulAdd(avgMod, one)).Sub(c015).StoreArray((*[4]float32)(tonality[i:]))
		one.Div(k.MulAdd(mod2, one)).Sub(c015).StoreArray((*[4]float32)(tonality2[i:]))

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

func reverse4F32(v archsimd.Float32x4) archsimd.Float32x4 {
	bits := v.ToBits()
	pairSwapped := bits.ConcatOdd(bits).InterleaveLo(bits.ConcatEven(bits))
	bytes := pairSwapped.ReshapeToUint8s()
	return bytes.ConcatShiftBytesRight(bytes, 8).ReshapeToUint32s().BitsToFloat32()
}

func analysisAtan2x4NEON(y, x archsimd.Float32x4) archsimd.Float32x4 {
	x2 := x.Mul(x)
	y2 := y.Mul(y)
	xy := x.Mul(y)
	zero := archsimd.Float32x4{}
	swap := x2.Less(y2)
	p := y2.IfElse(swap, x2)
	q := x2.IfElse(swap, y2)
	cA := archsimd.BroadcastFloat32x4(analysisAtanCA)
	cB := archsimd.BroadcastFloat32x4(analysisAtanCB)
	cC := archsimd.BroadcastFloat32x4(analysisAtanCC)
	num := xy.Neg().IfElse(swap, xy).Mul(cA.MulAdd(q, p))
	den := cB.MulAdd(q, p).Mul(cC.MulAdd(q, p))
	cE := archsimd.BroadcastFloat32x4(analysisAtanCE)
	negCE := archsimd.BroadcastFloat32x4(-analysisAtanCE)
	s1 := negCE.IfElse(y.Less(zero), cE)
	s2 := zero.IfElse(swap, negCE.IfElse(xy.Less(zero), cE))
	atan := num.Div(den).Add(s1).Sub(s2)
	return zero.IfElse(x2.Add(y2).Less(archsimd.BroadcastFloat32x4(1e-18)), atan)
}

func analysisFloat2Intx4NEON(x archsimd.Float32x4) archsimd.Float32x4 {
	return x.Round().ConvertToInt32().ConvertToFloat32()
}
