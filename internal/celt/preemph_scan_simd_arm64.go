//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// rawMaxMinScan folds x into celt_maxabs16's running MAX16/MIN16 extrema.
func rawMaxMinScan(x []float32, maxVal, minVal float32) (float32, float32) {
	n := len(x)
	if n < 8 || maxVal != maxVal || minVal != minVal {
		return rawMaxMinScanScalar(x, maxVal, minVal)
	}
	x = x[:n]
	hi := archsimd.BroadcastFloat32x4(maxVal)
	lo := archsimd.BroadcastFloat32x4(minVal)
	nan := archsimd.Int32x4{}
	i := 0
	for ; i+4 <= n; i += 4 {
		v := archsimd.LoadFloat32x4Array((*[4]float32)(x[i:]))
		hi = hi.Max(v)
		lo = lo.Min(v)
		nan = nan.Or(v.NotEqual(v).ToInt32x4())
	}
	if nan.GetElem(0)|nan.GetElem(1)|nan.GetElem(2)|nan.GetElem(3) != 0 {
		return rawMaxMinScanScalar(x, maxVal, minVal)
	}
	maxVal = max(max(hi.GetElem(0), hi.GetElem(1)), max(hi.GetElem(2), hi.GetElem(3)))
	minVal = min(min(lo.GetElem(0), lo.GetElem(1)), min(lo.GetElem(2), lo.GetElem(3)))
	return rawMaxMinScanScalar(x[i:], maxVal, minVal)
}

// preemphMono applies celt_preemphasis's single-tap filter to mono pcm.
func preemphMono(pcm, out []float32, coef, m float32) float32 {
	total := len(pcm)
	if total < 5 {
		return preemphMonoScalar(pcm, out, coef, m)
	}
	out = out[:total]
	out[0] = pcm[0]*float32(CELTSigScale) - m
	scale := archsimd.BroadcastFloat32x4(float32(CELTSigScale))
	coef4 := archsimd.BroadcastFloat32x4(coef)
	i := 1
	for ; i+4 <= total; i += 4 {
		scaled := archsimd.LoadFloat32x4Array((*[4]float32)(pcm[i:])).Mul(scale)
		prev := coef4.Mul(archsimd.LoadFloat32x4Array((*[4]float32)(pcm[i-1:])).Mul(scale))
		scaled.Sub(prev).StoreArray((*[4]float32)(out[i:]))
	}
	for ; i < total; i++ {
		scaled := pcm[i] * float32(CELTSigScale)
		out[i] = scaled - mul32(coef, pcm[i-1]*float32(CELTSigScale))
	}
	return coef * (pcm[total-1] * float32(CELTSigScale))
}

// preemphStereoPlanar applies celt_preemphasis's single-tap filter to
// interleaved stereo pcm and writes each channel to its planar output.
func preemphStereoPlanar(pcm, outL, outR []float32, coef float32, state [2]float32) [2]float32 {
	n := len(outL)
	if n < 5 {
		return preemphStereoPlanarScalar(pcm, outL, outR, coef, state)
	}
	pcm = pcm[:2*n]
	outR = outR[:n]
	outL[0] = pcm[0]*float32(CELTSigScale) - state[0]
	outR[0] = pcm[1]*float32(CELTSigScale) - state[1]
	scale := archsimd.BroadcastFloat32x4(float32(CELTSigScale))
	coef4 := archsimd.BroadcastFloat32x4(coef)
	i := 1
	for ; i+4 <= n; i += 4 {
		cur := pcm[2*i : 2*i+8]
		prev := pcm[2*i-2 : 2*i+6]
		cur0 := archsimd.LoadFloat32x4Array((*[4]float32)(cur)).ToBits()
		cur1 := archsimd.LoadFloat32x4Array((*[4]float32)(cur[4:])).ToBits()
		prev0 := archsimd.LoadFloat32x4Array((*[4]float32)(prev)).ToBits()
		prev1 := archsimd.LoadFloat32x4Array((*[4]float32)(prev[4:])).ToBits()
		curL := cur0.ConcatEven(cur1).BitsToFloat32()
		curR := cur0.ConcatOdd(cur1).BitsToFloat32()
		prevL := prev0.ConcatEven(prev1).BitsToFloat32()
		prevR := prev0.ConcatOdd(prev1).BitsToFloat32()
		curL.Mul(scale).Sub(coef4.Mul(prevL.Mul(scale))).StoreArray((*[4]float32)(outL[i:]))
		curR.Mul(scale).Sub(coef4.Mul(prevR.Mul(scale))).StoreArray((*[4]float32)(outR[i:]))
	}
	for ; i < n; i++ {
		outL[i] = pcm[2*i]*float32(CELTSigScale) - mul32(coef, pcm[2*i-2]*float32(CELTSigScale))
		outR[i] = pcm[2*i+1]*float32(CELTSigScale) - mul32(coef, pcm[2*i-1]*float32(CELTSigScale))
	}
	return [2]float32{
		coef * (pcm[2*n-2] * float32(CELTSigScale)),
		coef * (pcm[2*n-1] * float32(CELTSigScale)),
	}
}
