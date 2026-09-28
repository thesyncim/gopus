//go:build arm64 && nosimd

package celt

// mdctEncodeFMA32 computes a*b+c for the forward MDCT encoder fold via the
// Go arm64 backend's FMADDS contraction of fma32(a,b,c). The product and sum
// round once to float32, matching the contracted scalar C expression.
func mdctEncodeFMA32(a, b, c float32) float32 { return fma32(a, b, c) }
