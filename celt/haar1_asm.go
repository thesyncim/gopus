//go:build arm64 || amd64

package celt

// haar1Stride1Asm applies the Haar butterfly to n0 consecutive pairs of float64
// values using platform-specific scalar FP instructions. Computation is done in
// float32 precision (matching libopus) then widened back to float64 for storage.
//
//go:noescape
func haar1Stride1Asm(x []float64, n0 int)
