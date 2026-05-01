//go:build (arm64 || amd64) && !purego

package celt

// prefilterPitchXcorrFast uses the existing float64 vector kernel for the
// coarse quarter-rate search where tiny accumulation differences are tolerated.
func prefilterPitchXcorrFast(x, y, xcorr []float64, length, maxPitch int) {
	celtPitchXcorr(x, y, xcorr, length, maxPitch)
}
