//go:build arm64 && !purego

package celt

// haar1Stride1NEON applies the stride==1 Hadamard butterfly to n0 contiguous
// (even,odd) float32 pairs in place, using NEON LD2/ST2 deinterleave plus
// lane-wise FMUL then FADD/FSUB. Each product is bare and each add/sub is
// separate, so the result is bit-identical to the scalar fused path
// (haar1PairNorm) on this quality-gated build.
//
//go:noescape
func haar1Stride1NEON(x []float32, n0 int)
