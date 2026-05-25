//go:build amd64 && !purego

package celt

import "github.com/thesyncim/gopus/internal/cpufeat"

var amd64UseAVX2FMA = cpufeat.AMD64.HasAVX2 && cpufeat.AMD64.HasFMA

// Keep CELT prefilter/header-analysis decisions independent of host AVX/FMA
// exposure. These helpers feed encoded postfilter headers, and SIMD
// reassociation can flip tied pitch choices by one sample against the pinned
// amd64 fixtures.
var amd64UseAbsSumAVX2FMA = false
var amd64UsePitchXcorrAVX2FMA = false
var amd64UsePrefilterAVX2FMA = false
var amd64UsePitchAutocorrAVX2FMA = false
var amd64UseToneLPCCorrAVX2FMA = false

//go:noescape
func pvqSearchBestPosAVX(absX, y []float32, xy, yy float32, n int) int

func pvqSearchBestPos(absX, y []float32, xy, yy float32, n int) int {
	if amd64UseAVX2FMA {
		return pvqSearchBestPosAVX(absX, y, xy, yy, n)
	}
	return pvqSearchBestPosGeneric(absX, y, xy, yy, n)
}

//go:noescape
func pvqSearchPulseLoopAVX(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32)

func pvqSearchPulseLoop(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32) {
	if amd64UseAVX2FMA {
		return pvqSearchPulseLoopAVX(absX, y, iy, xy, yy, n, pulsesLeft)
	}
	return pvqSearchPulseLoopGeneric(absX, y, iy, xy, yy, n, pulsesLeft)
}

//go:noescape
func toneLPCCorrAVXFMA(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32)

func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	if amd64UseToneLPCCorrAVX2FMA && amd64UseAVX2FMA {
		return toneLPCCorrAVXFMA(x, cnt, delay, delay2)
	}
	return toneLPCCorrGeneric(x, cnt, delay, delay2)
}
