//go:build amd64 && !amd64.v3

package celt

func absSum(x []float64) float64 {
	return absSumGeneric(x)
}

func roundFloat64ToFloat32(x []float64) {
	roundFloat64ToFloat32Generic(x)
}

func celtPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	celtPitchXcorrGeneric(x, y, xcorr, length, maxPitch)
}

func prefilterInnerProd(x, y []float64, length int) float64 {
	return prefilterInnerProdGeneric(x, y, length)
}

func prefilterDualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	return prefilterDualInnerProdGeneric(x, y1, y2, length)
}

func pvqSearchBestPos(absX, y []float32, xy, yy float64, n int) int {
	return pvqSearchBestPosGeneric(absX, y, xy, yy, n)
}

func pvqSearchPulseLoop(absX, y []float32, iy []int, xy, yy float64, n, pulsesLeft int) (float64, float64) {
	return pvqSearchPulseLoopGeneric(absX, y, iy, xy, yy, n, pulsesLeft)
}

func pvqExtractAbsSign(x []float64, absX []float32, y []float32, signx []int, iy []int, n int) {
	pvqExtractAbsSignGeneric(x, absX, y, signx, iy, n)
}

func expRotation1Stride2(x []float64, length int, c, s float64) {
	expRotation1Stride2Generic(x, length, c, s)
}

func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64 {
	return transientEnergyPairsGeneric(tmp, x2out, len2)
}

func pitchAutocorr5(lp []float64, length int, ac *[5]float64) {
	pitchAutocorr5Generic(lp, length, ac)
}

func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	return toneLPCCorrGeneric(x, cnt, delay, delay2)
}

func prefilterPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	prefilterPitchXcorrGeneric(x, y, xcorr, length, maxPitch)
}
