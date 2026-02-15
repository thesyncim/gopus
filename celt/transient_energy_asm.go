//go:build arm64 || amd64

package celt

// transientEnergyPairs computes energy of sample pairs from float64 input:
//   x2out[i] = float32(tmp[2*i])^2 + float32(tmp[2*i+1])^2
// and returns the sum of all x2out values (mean).
// tmp must have at least 2*len2 elements.
//
//go:noescape
func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64
