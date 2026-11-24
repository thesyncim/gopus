//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// toneSumStereo computes dst[k] = srcL[k] + srcR[k] four float32 lanes at a
// time on ARM64 NEON.
func toneSumStereo(dst, srcL, srcR []float32) {
	n := len(dst)
	dst = dst[:n:n]
	srcL = srcL[:n:n]
	srcR = srcR[:n:n]
	i := 0
	for ; i+4 <= n; i += 4 {
		a := archsimd.LoadFloat32x4Array((*[4]float32)(srcL[i : i+4]))
		b := archsimd.LoadFloat32x4Array((*[4]float32)(srcR[i : i+4]))
		a.Add(b).StoreArray((*[4]float32)(dst[i : i+4]))
	}
	for ; i < n; i++ {
		dst[i] = srcL[i] + srcR[i]
	}
}

// transientStereoForwardBackward runs the stereo high-pass filter, forward
// masking, and backward masking of transient_analysis with left and right
// channels paired in the low two lanes of Float32x4 vectors. Every lane
// performs the exact scalar FSUB/FMADD sequence of transient_analysis.
func transientStereoForwardBackward(srcL, srcR, energyL, energyR []float32, forwardDecay, forwardRetain float32) (meanL, meanR, maxEL, maxER float32) {
	const (
		hpFeedback     = float32(0.5)
		backwardRetain = float32(0.875)
		backwardScale  = float32(0.125)
		warmupPairs    = 6
	)
	len2 := len(energyL)
	if len2 == 0 {
		return 0, 0, 0, 0
	}
	srcL = srcL[: 2*len2 : 2*len2]
	srcR = srcR[: 2*len2 : 2*len2]
	energyL = energyL[:len2:len2]
	energyR = energyR[:len2:len2]

	vHPFb := archsimd.BroadcastFloat32x4(hpFeedback)
	vFwdRetain := archsimd.BroadcastFloat32x4(forwardRetain)
	vFwdDecay := archsimd.BroadcastFloat32x4(forwardDecay)
	var hp0, hp1, mask, mean archsimd.Float32x4

	warm := min(warmupPairs, len2)
	for i := range warm {
		idx := 2 * i
		x0 := archsimd.LoadFloat32x4Array(&[4]float32{srcL[idx], srcR[idx], 0, 0})
		x1 := archsimd.LoadFloat32x4Array(&[4]float32{srcL[idx+1], srcR[idx+1], 0, 0})
		hp00 := hp0
		hp0 = vHPFb.MulAdd(hp1, hp0.Sub(x0))
		hp1 = x0.Sub(hp00)
		hp00 = hp0
		hp0 = vHPFb.MulAdd(hp1, hp0.Sub(x1))
		hp1 = x1.Sub(hp00)
		energyL[i] = 0
		energyR[i] = 0
	}

	i := warm
	for ; i+2 <= len2; i += 2 {
		idx := 2 * i
		vl := archsimd.LoadFloat32x4Array((*[4]float32)(srcL[idx : idx+4])).ToBits()
		vr := archsimd.LoadFloat32x4Array((*[4]float32)(srcR[idx : idx+4])).ToBits()
		p01 := vl.InterleaveLo(vr)
		p23 := vl.InterleaveHi(vr)
		p01q := p01.ReshapeToUint64s()
		p23q := p23.ReshapeToUint64s()
		x0 := p01.BitsToFloat32()
		x1 := p01q.InterleaveHi(p01q).ReshapeToUint32s().BitsToFloat32()
		x2 := p23.BitsToFloat32()
		x3 := p23q.InterleaveHi(p23q).ReshapeToUint32s().BitsToFloat32()

		y0 := hp0.Add(x0)
		hp00 := hp0
		hp0 = vHPFb.MulAdd(hp1, hp0.Sub(x0))
		hp1 = x0.Sub(hp00)

		y1 := hp0.Add(x1)
		hp00 = hp0
		hp0 = vHPFb.MulAdd(hp1, hp0.Sub(x1))
		hp1 = x1.Sub(hp00)

		pair0 := y1.MulAdd(y1, y0.Mul(y0))
		mean = mean.Add(pair0)
		mask = vFwdRetain.MulAdd(mask, pair0)
		e0 := vFwdDecay.Mul(mask)
		energyL[i] = e0.GetElem(0)
		energyR[i] = e0.GetElem(1)

		y2 := hp0.Add(x2)
		hp00 = hp0
		hp0 = vHPFb.MulAdd(hp1, hp0.Sub(x2))
		hp1 = x2.Sub(hp00)

		y3 := hp0.Add(x3)
		hp00 = hp0
		hp0 = vHPFb.MulAdd(hp1, hp0.Sub(x3))
		hp1 = x3.Sub(hp00)

		pair1 := y3.MulAdd(y3, y2.Mul(y2))
		mean = mean.Add(pair1)
		mask = vFwdRetain.MulAdd(mask, pair1)
		e1 := vFwdDecay.Mul(mask)
		energyL[i+1] = e1.GetElem(0)
		energyR[i+1] = e1.GetElem(1)
	}
	for ; i < len2; i++ {
		idx := 2 * i
		x0 := archsimd.LoadFloat32x4Array(&[4]float32{srcL[idx], srcR[idx], 0, 0})
		x1 := archsimd.LoadFloat32x4Array(&[4]float32{srcL[idx+1], srcR[idx+1], 0, 0})
		y0 := hp0.Add(x0)
		hp00 := hp0
		hp0 = vHPFb.MulAdd(hp1, hp0.Sub(x0))
		hp1 = x0.Sub(hp00)
		y1 := hp0.Add(x1)
		hp00 = hp0
		hp0 = vHPFb.MulAdd(hp1, hp0.Sub(x1))
		hp1 = x1.Sub(hp00)
		pair := y1.MulAdd(y1, y0.Mul(y0))
		mean = mean.Add(pair)
		mask = vFwdRetain.MulAdd(mask, pair)
		e := vFwdDecay.Mul(mask)
		energyL[i] = e.GetElem(0)
		energyR[i] = e.GetElem(1)
	}
	meanL, meanR = mean.GetElem(0), mean.GetElem(1)

	vBackRetain := archsimd.BroadcastFloat32x4(backwardRetain)
	vBackScale := archsimd.BroadcastFloat32x4(backwardScale)
	var backMask, maxE archsimd.Float32x4
	k := len2 - 1
	for ; k >= 3; k -= 4 {
		el := archsimd.LoadFloat32x4Array((*[4]float32)(energyL[k-3 : k+1])).ToBits()
		er := archsimd.LoadFloat32x4Array((*[4]float32)(energyR[k-3 : k+1])).ToBits()
		p01 := el.InterleaveLo(er)
		p23 := el.InterleaveHi(er)
		p01q := p01.ReshapeToUint64s()
		p23q := p23.ReshapeToUint64s()
		e3 := p23q.InterleaveHi(p23q).ReshapeToUint32s().BitsToFloat32()
		e2 := p23.BitsToFloat32()
		e1 := p01q.InterleaveHi(p01q).ReshapeToUint32s().BitsToFloat32()
		e0 := p01.BitsToFloat32()

		backMask = vBackRetain.MulAdd(backMask, e3)
		o3 := vBackScale.Mul(backMask)
		maxE = maxE.IfElse(maxE.Greater(o3), o3)

		backMask = vBackRetain.MulAdd(backMask, e2)
		o2 := vBackScale.Mul(backMask)
		maxE = maxE.IfElse(maxE.Greater(o2), o2)

		backMask = vBackRetain.MulAdd(backMask, e1)
		o1 := vBackScale.Mul(backMask)
		maxE = maxE.IfElse(maxE.Greater(o1), o1)

		backMask = vBackRetain.MulAdd(backMask, e0)
		o0 := vBackScale.Mul(backMask)
		maxE = maxE.IfElse(maxE.Greater(o0), o0)

		// Deinterleave o0..o3 back to energyL[k-3:k+1] and energyR[k-3:k+1].
		o01 := o0.ToBits().ReshapeToUint64s().InterleaveLo(o1.ToBits().ReshapeToUint64s()).ReshapeToUint32s()
		o23 := o2.ToBits().ReshapeToUint64s().InterleaveLo(o3.ToBits().ReshapeToUint64s()).ReshapeToUint32s()
		o01.ConcatEven(o23).BitsToFloat32().StoreArray((*[4]float32)(energyL[k-3 : k+1]))
		o01.ConcatOdd(o23).BitsToFloat32().StoreArray((*[4]float32)(energyR[k-3 : k+1]))
	}
	for ; k >= 0; k-- {
		ek := archsimd.LoadFloat32x4Array(&[4]float32{energyL[k], energyR[k], 0, 0})
		backMask = vBackRetain.MulAdd(backMask, ek)
		ok := vBackScale.Mul(backMask)
		energyL[k] = ok.GetElem(0)
		energyR[k] = ok.GetElem(1)
		maxE = maxE.IfElse(maxE.Greater(ok), ok)
	}
	maxEL, maxER = maxE.GetElem(0), maxE.GetElem(1)
	return meanL, meanR, maxEL, maxER
}
