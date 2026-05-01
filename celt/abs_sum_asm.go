//go:build arm64 && !purego

package celt

//go:noescape
func absSum(x []float64) float64
