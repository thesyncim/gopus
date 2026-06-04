package celt

import "math"

func absSum(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += math.Abs(v)
	}
	return sum
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
