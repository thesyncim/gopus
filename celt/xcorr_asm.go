//go:build arm64 || amd64

package celt

//go:noescape
func celtInnerProd(x, y []float64, length int) float64

//go:noescape
func dualInnerProd(x, y1, y2 []float64, length int) (float64, float64)

//go:noescape
func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int)
