package celt

import "math"

// mdctFMA32 computes a*b+c with the double-precision math.FMA and converts the
// result to float32. The double FMA rounds once and the conversion rounds
// again; when the exact sum needs more than 53 bits the two roundings can
// differ from the single float32 rounding of an FMADDS or VFMADD*PS (for example
// 0x3fcca800*0x3f979800 + 0xa20c2545 gives 0x3ff26138 instead of 0x3ff26137).
// Paths that must match a hardware float32 FMA bit for bit use fma32 on arm64
// or an archsimd MulAdd instead.
func mdctFMA32(a, b, c float32) float32 {
	return float32(math.FMA(float64(a), float64(b), float64(c)))
}
