//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"unsafe"

	"simd/archsimd"
)

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
	p := unsafe.Pointer(unsafe.SliceData(x))
	hi := broadcastF32x4Arch(maxVal)
	lo := broadcastF32x4Arch(minVal)
	nan := archsimd.Int32x4{}
	i := 0
	for ; i+4 <= n; i += 4 {
		v := loadF32x4(unsafe.Add(p, 4*i))
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

// preemphInterleaved applies celt_preemphasis's single-tap filter to
// channels-interleaved pcm: out[i] = s[i] - coef*s[i-channels] with
// s = CELT_SIG_SCALE*pcm, the first channels samples continuing from state.
// Each output needs only the previous scaled sample of its channel, so four
// outputs run per step with the scalar loop's exact products and
// differences. It returns the updated per-channel state.
func preemphInterleaved(pcm, out []float32, total, channels int, coef float32, state [2]float32) [2]float32 {
	if total < channels+4 || !archsimd.X86.AVX() {
		return preemphInterleavedScalar(pcm, out, total, channels, coef, state)
	}
	return preemphInterleavedAVX(pcm, out, total, channels, coef, state)
}

//go:noinline
func preemphInterleavedAVX(pcm, out []float32, total, channels int, coef float32, state [2]float32) [2]float32 {
	pcm = pcm[:total]
	out = out[:total]
	for c := range channels {
		scaled := pcm[c] * float32(CELTSigScale)
		out[c] = scaled - state[c]
	}
	scale := broadcastF32x4Arch(float32(CELTSigScale))
	coef4 := broadcastF32x4Arch(coef)
	pp := unsafe.Pointer(unsafe.SliceData(pcm))
	op := unsafe.Pointer(unsafe.SliceData(out))
	i := channels
	for ; i+4 <= total; i += 4 {
		scaled := loadF32x4(unsafe.Add(pp, 4*i)).Mul(scale)
		prev := coef4.Mul(loadF32x4(unsafe.Add(pp, 4*(i-channels))).Mul(scale))
		storeF32x4(unsafe.Add(op, 4*i), scaled.Sub(prev))
	}
	for ; i < total; i++ {
		scaled := pcm[i] * float32(CELTSigScale)
		out[i] = scaled - mul32(coef, pcm[i-channels]*float32(CELTSigScale))
	}
	for c := range channels {
		state[c] = coef * (pcm[total-channels+c] * float32(CELTSigScale))
	}
	return state
}
