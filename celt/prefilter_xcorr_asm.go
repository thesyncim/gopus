//go:build arm64 || amd64

package celt

//go:noescape
func prefilterPitchXcorr(x, y, xcorr []float64, length, maxPitch int)
