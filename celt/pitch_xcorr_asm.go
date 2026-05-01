//go:build arm64 && !purego

package celt

//go:noescape
func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int)
