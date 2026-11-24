//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var preemphUsesAVX2 = archsimd.X86.AVX2()

// rawMaxMinScan folds x into celt_maxabs16's running MAX16/MIN16 extrema.
// Without NaN samples the sequential extrema are the plain maximum and
// minimum, so eight or four lanes reduce them in any order; the only
// difference is the sign of an equal zero, which no caller observes. A NaN
// makes the sequential result depend on its position, so that input takes the
// scalar scan.
func rawMaxMinScan(x []float32, maxVal, minVal float32) (float32, float32) {
	if maxVal != maxVal || minVal != minVal {
		return rawMaxMinScanScalar(x, maxVal, minVal)
	}
	if preemphUsesAVX2 && len(x) >= 16 {
		return rawMaxMinScanAVX2(x, maxVal, minVal)
	}
	if !archsimd.X86.AVX() {
		return rawMaxMinScanScalar(x, maxVal, minVal)
	}
	return rawMaxMinScanAVX(x, maxVal, minVal)
}

//go:noinline
func rawMaxMinScanAVX2(x []float32, maxVal, minVal float32) (float32, float32) {
	n := len(x)
	x = x[:n]
	hi := archsimd.BroadcastFloat32x8(maxVal)
	lo := archsimd.BroadcastFloat32x8(minVal)
	nan := archsimd.Int32x8{}
	i := 0
	for ; i+8 <= n; i += 8 {
		v := archsimd.LoadFloat32x8Array((*[8]float32)(x[i:]))
		hi = hi.Max(v)
		lo = lo.Min(v)
		nan = nan.Or(v.IsNaN().ToInt32x8())
	}
	nan4 := nan.GetLo().Or(nan.GetHi())
	hi4 := hi.GetLo().Max(hi.GetHi())
	lo4 := lo.GetLo().Min(lo.GetHi())
	archsimd.ClearAVXUpperBits()
	if nan4.GetElem(0)|nan4.GetElem(1)|nan4.GetElem(2)|nan4.GetElem(3) != 0 {
		return rawMaxMinScanScalar(x, maxVal, minVal)
	}
	maxVal = max(max(hi4.GetElem(0), hi4.GetElem(1)), max(hi4.GetElem(2), hi4.GetElem(3)))
	minVal = min(min(lo4.GetElem(0), lo4.GetElem(1)), min(lo4.GetElem(2), lo4.GetElem(3)))
	return rawMaxMinScanScalar(x[i:], maxVal, minVal)
}

//go:noinline
func rawMaxMinScanAVX(x []float32, maxVal, minVal float32) (float32, float32) {
	n := len(x)
	if n < 8 {
		return rawMaxMinScanScalar(x, maxVal, minVal)
	}
	x = x[:n]
	hi := broadcastF32x4Arch(maxVal)
	lo := broadcastF32x4Arch(minVal)
	nan := archsimd.Int32x4{}
	i := 0
	for ; i+4 <= n; i += 4 {
		v := archsimd.LoadFloat32x4Array((*[4]float32)(x[i:]))
		hi = hi.Max(v)
		lo = lo.Min(v)
		nan = nan.Or(v.IsNaN().ToInt32x4())
	}
	if nan.GetElem(0)|nan.GetElem(1)|nan.GetElem(2)|nan.GetElem(3) != 0 {
		return rawMaxMinScanScalar(x, maxVal, minVal)
	}
	maxVal = max(max(hi.GetElem(0), hi.GetElem(1)), max(hi.GetElem(2), hi.GetElem(3)))
	minVal = min(min(lo.GetElem(0), lo.GetElem(1)), min(lo.GetElem(2), lo.GetElem(3)))
	return rawMaxMinScanScalar(x[i:], maxVal, minVal)
}

// preemphMono applies celt_preemphasis's single-tap filter to mono pcm:
// out[i] = s[i] - coef*s[i-1] with s = CELT_SIG_SCALE*pcm, the first output
// continuing from the carried m. Each output needs only the previous scaled
// sample, so eight outputs run per step on AVX2 (or four on AVX1) with the
// scalar loop's exact products and differences. It returns the updated m.
func preemphMono(pcm, out []float32, coef, m float32) float32 {
	total := len(pcm)
	if total < 5 {
		return preemphMonoScalar(pcm, out, coef, m)
	}
	if preemphUsesAVX2 && total >= 9 {
		return preemphMonoAVX2(pcm, out, coef, m)
	}
	if !archsimd.X86.AVX() {
		return preemphMonoScalar(pcm, out, coef, m)
	}
	return preemphMonoAVX(pcm, out, coef, m)
}

//go:noinline
func preemphMonoAVX2(pcm, out []float32, coef, m float32) float32 {
	total := len(pcm)
	out = out[:total]
	out[0] = pcm[0]*float32(CELTSigScale) - m
	scale := archsimd.BroadcastFloat32x8(float32(CELTSigScale))
	coef8 := archsimd.BroadcastFloat32x8(coef)
	i := 1
	for ; i+8 <= total; i += 8 {
		scaled := archsimd.LoadFloat32x8Array((*[8]float32)(pcm[i:])).Mul(scale)
		prev := coef8.Mul(archsimd.LoadFloat32x8Array((*[8]float32)(pcm[i-1:])).Mul(scale))
		scaled.Sub(prev).StoreArray((*[8]float32)(out[i:]))
	}
	archsimd.ClearAVXUpperBits()
	if i+4 <= total {
		scale4 := broadcastF32x4Arch(float32(CELTSigScale))
		coef4 := broadcastF32x4Arch(coef)
		scaled := archsimd.LoadFloat32x4Array((*[4]float32)(pcm[i:])).Mul(scale4)
		prev := coef4.Mul(archsimd.LoadFloat32x4Array((*[4]float32)(pcm[i-1:])).Mul(scale4))
		scaled.Sub(prev).StoreArray((*[4]float32)(out[i:]))
		i += 4
	}
	for ; i < total; i++ {
		scaled := pcm[i] * float32(CELTSigScale)
		out[i] = scaled - mul32(coef, pcm[i-1]*float32(CELTSigScale))
	}
	return coef * (pcm[total-1] * float32(CELTSigScale))
}

//go:noinline
func preemphMonoAVX(pcm, out []float32, coef, m float32) float32 {
	total := len(pcm)
	out = out[:total]
	out[0] = pcm[0]*float32(CELTSigScale) - m
	scale := broadcastF32x4Arch(float32(CELTSigScale))
	coef4 := broadcastF32x4Arch(coef)
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
// interleaved stereo pcm and writes each channel to its planar output:
// out_c[i] = s_c[i] - coef*s_c[i-1] with s = CELT_SIG_SCALE*pcm, the first
// outputs continuing from state. Each step loads four current and four
// previous sample pairs, splits them into channels, and forms four outputs
// per channel with the scalar loop's exact products and differences. It
// returns the updated per-channel m.
func preemphStereoPlanar(pcm, outL, outR []float32, coef float32, state [2]float32) [2]float32 {
	n := len(outL)
	if n < 5 || !archsimd.X86.AVX() {
		return preemphStereoPlanarScalar(pcm, outL, outR, coef, state)
	}
	return preemphStereoPlanarAVX(pcm, outL, outR, coef, state)
}

//go:noinline
func preemphStereoPlanarAVX(pcm, outL, outR []float32, coef float32, state [2]float32) [2]float32 {
	n := len(outL)
	pcm = pcm[:2*n]
	outR = outR[:n]
	outL[0] = pcm[0]*float32(CELTSigScale) - state[0]
	outR[0] = pcm[1]*float32(CELTSigScale) - state[1]
	scale := broadcastF32x4Arch(float32(CELTSigScale))
	coef4 := broadcastF32x4Arch(coef)
	i := 1
	for ; i+4 <= n; i += 4 {
		cur := pcm[2*i : 2*i+8]
		prev := pcm[2*i-2 : 2*i+6]
		cur0 := archsimd.LoadFloat32x4Array((*[4]float32)(cur))
		cur1 := archsimd.LoadFloat32x4Array((*[4]float32)(cur[4:]))
		prev0 := archsimd.LoadFloat32x4Array((*[4]float32)(prev))
		prev1 := archsimd.LoadFloat32x4Array((*[4]float32)(prev[4:]))
		curL := cur0.ConcatPermuteScalars(0, 2, 4, 6, cur1)
		curR := cur0.ConcatPermuteScalars(1, 3, 5, 7, cur1)
		prevL := prev0.ConcatPermuteScalars(0, 2, 4, 6, prev1)
		prevR := prev0.ConcatPermuteScalars(1, 3, 5, 7, prev1)
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
