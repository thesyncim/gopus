//go:build arm64 || amd64

package celt

//go:noescape
func prefilterInnerProd(x, y []float64, length int) float64

//go:noescape
func prefilterDualInnerProd(x, y1, y2 []float64, length int) (float64, float64)
