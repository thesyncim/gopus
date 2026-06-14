//go:build arm64 && purego

package celt

// mdctEncodeFMA32 computes a*b+c for the forward MDCT encoder fold using the
// Go arm64 backend's FMADDS contraction of fma32(a,b,c). This avoids the
// expensive float64 round-trip (FCVT+FMADDD+FCVT) of math.FMA while remaining
// bit-identical to the libopus NEON vfmaq_f32 path for the encoder, which is
// quality-gated rather than bit-exact. The decoder IMDCT pre-rotation keeps
// mdctFMA32 (math.FMA) to preserve existing parity fixtures.
func mdctEncodeFMA32(a, b, c float32) float32 { return fma32(a, b, c) }
