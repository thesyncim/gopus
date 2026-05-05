//go:build amd64 && !purego

package celt

import "github.com/thesyncim/gopus/internal/cpufeat"

var amd64UseAVX2FMA = cpufeat.AMD64.HasAVX2 && cpufeat.AMD64.HasFMA

// Keep CELT prefilter decisions independent of host AVX/FMA exposure. These
// helpers feed encoded postfilter headers, and SIMD reassociation can flip tied
// pitch choices by one sample against the pinned amd64 fixtures.
var amd64UsePrefilterAVX2FMA = false

//go:noescape
func absSumAVX(x []float64) float64

func absSum(x []float64) float64 {
	if amd64UseAVX2FMA {
		return absSumAVX(x)
	}
	return absSumGeneric(x)
}

//go:noescape
func roundFloat64ToFloat32AVX(x []float64)

func roundFloat64ToFloat32(x []float64) {
	if amd64UseAVX2FMA {
		roundFloat64ToFloat32AVX(x)
		return
	}
	roundFloat64ToFloat32Generic(x)
}

//go:noescape
func celtPitchXcorrAVX2FMA(x []float64, y []float64, xcorr []float64, length, maxPitch int)

func celtPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	if amd64UseAVX2FMA {
		celtPitchXcorrAVX2FMA(x, y, xcorr, length, maxPitch)
		return
	}
	celtPitchXcorrGeneric(x, y, xcorr, length, maxPitch)
}

//go:noescape
func prefilterInnerProdAVXFMA(x, y []float64, length int) float64

func prefilterInnerProd(x, y []float64, length int) float64 {
	if amd64UsePrefilterAVX2FMA && amd64UseAVX2FMA {
		return prefilterInnerProdAVXFMA(x, y, length)
	}
	return prefilterInnerProdGeneric(x, y, length)
}

//go:noescape
func prefilterDualInnerProdAVXFMA(x, y1, y2 []float64, length int) (float64, float64)

func prefilterDualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	if amd64UsePrefilterAVX2FMA && amd64UseAVX2FMA {
		return prefilterDualInnerProdAVXFMA(x, y1, y2, length)
	}
	return prefilterDualInnerProdGeneric(x, y1, y2, length)
}

//go:noescape
func pvqSearchBestPosAVX(absX, y []float32, xy, yy float64, n int) int

func pvqSearchBestPos(absX, y []float32, xy, yy float64, n int) int {
	if amd64UseAVX2FMA {
		return pvqSearchBestPosAVX(absX, y, xy, yy, n)
	}
	return pvqSearchBestPosGeneric(absX, y, xy, yy, n)
}

//go:noescape
func pvqSearchPulseLoopAVX(absX, y []float32, iy []int, xy, yy float64, n, pulsesLeft int) (float64, float64)

func pvqSearchPulseLoop(absX, y []float32, iy []int, xy, yy float64, n, pulsesLeft int) (float64, float64) {
	if amd64UseAVX2FMA {
		return pvqSearchPulseLoopAVX(absX, y, iy, xy, yy, n, pulsesLeft)
	}
	return pvqSearchPulseLoopGeneric(absX, y, iy, xy, yy, n, pulsesLeft)
}

//go:noescape
func pvqExtractAbsSignAVX(x []float64, absX []float32, y []float32, signx []byte, iy []int, n int)

func pvqExtractAbsSign(x []float64, absX []float32, y []float32, signx []byte, iy []int, n int) {
	if amd64UseAVX2FMA {
		pvqExtractAbsSignAVX(x, absX, y, signx, iy, n)
		return
	}
	pvqExtractAbsSignGeneric(x, absX, y, signx, iy, n)
}

//go:noescape
func expRotation1Stride2AVXFMA(x []float64, length int, c, s float64)

func expRotation1Stride2(x []float64, length int, c, s float64) {
	expRotation1Stride2Generic(x, length, c, s)
}

//go:noescape
func transientEnergyPairsAVX(tmp []float64, x2out []float32, len2 int) float64

func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64 {
	if amd64UseAVX2FMA {
		return transientEnergyPairsAVX(tmp, x2out, len2)
	}
	return transientEnergyPairsGeneric(tmp, x2out, len2)
}

//go:noescape
func pitchAutocorr5AVXFMA(lp []float64, length int, ac *[5]float64)

func pitchAutocorr5(lp []float64, length int, ac *[5]float64) {
	if amd64UseAVX2FMA {
		pitchAutocorr5AVXFMA(lp, length, ac)
		return
	}
	pitchAutocorr5Generic(lp, length, ac)
}

//go:noescape
func toneLPCCorrAVXFMA(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32)

func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	if amd64UseAVX2FMA {
		return toneLPCCorrAVXFMA(x, cnt, delay, delay2)
	}
	return toneLPCCorrGeneric(x, cnt, delay, delay2)
}

//go:noescape
func prefilterPitchXcorrAVX2FMA(x, y, xcorr []float64, length, maxPitch int)

func prefilterPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	if amd64UsePrefilterAVX2FMA && amd64UseAVX2FMA {
		prefilterPitchXcorrAVX2FMA(x, y, xcorr, length, maxPitch)
		return
	}
	prefilterPitchXcorrGeneric(x, y, xcorr, length, maxPitch)
}
