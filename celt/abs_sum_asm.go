//go:build arm64 || amd64

package celt

//go:noescape
func absSum(x []float64) float64
