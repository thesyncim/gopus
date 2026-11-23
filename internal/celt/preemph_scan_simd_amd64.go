//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// rawMaxMinScan folds x into celt_maxabs16's running MAX16/MIN16 extrema.
// Without NaN samples the sequential extrema are the plain maximum and
// minimum, so four lanes reduce them in any order; the only difference is
// the sign of an equal zero, which no caller observes. A NaN makes the
// sequential result depend on its position, so that input takes the scalar
// scan.
func rawMaxMinScan(x []float32, maxVal, minVal float32) (float32, float32) {
	if !archsimd.X86.AVX() || maxVal != maxVal || minVal != minVal {
		return rawMaxMinScanScalar(x, maxVal, minVal)
	}
	return rawMaxMinScanAVX(x, maxVal, minVal)
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
// sample, so four outputs run per step with the scalar loop's exact products
// and differences. It returns the updated m.
func preemphMono(pcm, out []float32, coef, m float32) float32 {
	total := len(pcm)
	if total < 5 || !archsimd.X86.AVX() {
		return preemphMonoScalar(pcm, out, coef, m)
	}
	return preemphMonoAVX(pcm, out, coef, m)
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
