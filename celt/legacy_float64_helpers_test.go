package celt

import "math"

func absSum(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += math.Abs(v)
	}
	return sum
}

func absSumGeneric(x []float64) float64 {
	return absSum(x)
}

func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int) {
	refPitchXcorr(x, y, xcorr, length, maxPitch)
}

func celtPitchXcorrGeneric(x []float64, y []float64, xcorr []float64, length, maxPitch int) {
	refPitchXcorr(x, y, xcorr, length, maxPitch)
}

func prefilterInnerProd(x, y []float64, length int) float64 {
	return asmPrefilterInnerProdRef(x, y, length)
}

func prefilterInnerProdGeneric(x, y []float64, length int) float64 {
	return asmPrefilterInnerProdRef(x, y, length)
}

func prefilterDualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	return prefilterDualInnerProdRef(x, y1, y2, length)
}

func prefilterDualInnerProdGeneric(x, y1, y2 []float64, length int) (float64, float64) {
	return prefilterDualInnerProdRef(x, y1, y2, length)
}

func prefilterPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	asmPrefilterPitchXcorrRef(x, y, xcorr, length, maxPitch)
}

func prefilterPitchXcorrGeneric(x, y, xcorr []float64, length, maxPitch int) {
	asmPrefilterPitchXcorrRef(x, y, xcorr, length, maxPitch)
}

func prefilterPitchXcorrFast(x, y, xcorr []float64, length, maxPitch int) {
	asmPrefilterPitchXcorrRef(x, y, xcorr, length, maxPitch)
}

func expRotation1Stride2(x []float64, length int, c, s float64) {
	expRotation1Stride2Generic(x, length, c, s)
}

func expRotation1Stride2Generic(x []float64, length int, c, s float64) {
	ms := -s
	end := length - 2
	i := 0
	for ; i+1 < end; i += 2 {
		x1 := x[i]
		x2 := x[i+2]
		x[i+2] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2

		x3 := x[i+1]
		x4 := x[i+3]
		x[i+3] = c*x4 + s*x3
		x[i+1] = c*x3 + ms*x4
	}
	for ; i < end; i++ {
		x1 := x[i]
		x2 := x[i+2]
		x[i+2] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2
	}
	i = length - 5
	for ; i-1 >= 0; i -= 2 {
		x1 := x[i]
		x2 := x[i+2]
		x[i+2] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2

		x3 := x[i-1]
		x4 := x[i+1]
		x[i+1] = c*x4 + s*x3
		x[i-1] = c*x3 + ms*x4
	}
	for ; i >= 0; i-- {
		x1 := x[i]
		x2 := x[i+2]
		x[i+2] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2
	}
}

func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64 {
	return asmTransientEnergyPairsRef(tmp, x2out, len2)
}

func transientEnergyPairsGeneric(tmp []float64, x2out []float32, len2 int) float64 {
	return asmTransientEnergyPairsRef(tmp, x2out, len2)
}

func pitchAutocorr5(lp []float64, length int, ac *[5]float64) {
	pitchAutocorr5Ref(lp, length, ac)
}

func pitchAutocorr5Generic(lp []float64, length int, ac *[5]float64) {
	pitchAutocorr5Ref(lp, length, ac)
}
