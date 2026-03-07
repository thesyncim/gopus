//go:build !arm64 && !amd64

package celt

func prefilterPitchXcorrFast(x, y, xcorr []float64, length, maxPitch int) {
	prefilterPitchXcorr(x, y, xcorr, length, maxPitch)
}
