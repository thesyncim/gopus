package celt

import "math"

// Shared test helpers for the celt package. These adapt the float64-based test
// fixtures used throughout the suite to the float32/int types the production
// code consumes, and wrap a few reference kernels behind float64 signatures.

// Slice conversion helpers.

func copyFloat64ToSig(dst []celtSig, src []float64) {
	n := min(len(dst), len(src))
	for i := range n {
		dst[i] = celtSig(src[i])
	}
}

func int32SliceForTest(src []int) []int32 {
	dst := make([]int32, len(src))
	for i, v := range src {
		dst[i] = int32(v)
	}
	return dst
}

// Math approximation wrappers (float64 in/out around the float32 kernels).

func celtAtan2pNorm(y, x float64) float64 {
	return float64(celtAtan2pNormF32(float32(y), float32(x)))
}

func celtAtanNorm(x float64) float64 {
	return float64(celtAtanNormF32(float32(x)))
}

func celtCosNorm2(x float64) float64 {
	return float64(celtCosNorm2F32(float32(x)))
}

// Reduction / correlation reference wrappers.

func absSum(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += math.Abs(v)
	}
	return sum
}

func sumOfSquaresF64toF32(x []float64, n int) float64 {
	if n <= 0 {
		return 0
	}
	if n > len(x) {
		n = len(x)
	}
	tmp := make([]float32, n)
	for i := range tmp {
		tmp[i] = float32(x[i])
	}
	return float64(celtInnerProdF32LibopusOrder(tmp))
}

func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	refPitchXcorr(x, y, xcorr, length, maxPitch)
}

func prefilterInnerProd(x, y []float64, length int) float64 {
	return asmPrefilterInnerProdRef(x, y, length)
}

func prefilterDualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	return prefilterDualInnerProdRef(x, y1, y2, length)
}

func prefilterPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	asmPrefilterPitchXcorrRef(x, y, xcorr, length, maxPitch)
}

func prefilterPitchXcorrFast(x, y, xcorr []float64, length, maxPitch int) {
	asmPrefilterPitchXcorrRef(x, y, xcorr, length, maxPitch)
}

func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64 {
	return asmTransientEnergyPairsRef(tmp, x2out, len2)
}

func pitchAutocorr5(lp []float64, length int, ac *[5]float64) {
	pitchAutocorr5Ref(lp, length, ac)
}

// antiCollapse adapts the float64 test fixtures to the GLog/int32 signature of
// antiCollapseGLog.
func antiCollapse(
	coeffsL, coeffsR []celtNorm,
	collapse []byte,
	lm int,
	channels int,
	start, end int,
	logE, prev1LogE, prev2LogE []float64,
	pulses []int,
	seed uint32,
) {
	logEGLog := float64sToGLogs(logE)
	prev1GLog := float64sToGLogs(prev1LogE)
	prev2GLog := float64sToGLogs(prev2LogE)
	pulses32 := make([]int32, len(pulses))
	for i, pulse := range pulses {
		pulses32[i] = int32(pulse)
	}
	antiCollapseGLog(coeffsL, coeffsR, collapse, lm, channels, start, end, logEGLog, prev1GLog, prev2GLog, pulses32, seed)
}
