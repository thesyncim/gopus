//go:build !arm64 || !purego

package celt

// mdctEncodeFMA32 delegates to mdctFMA32 (math.FMA float64 round-trip) on all
// targets where the encoder and decoder share the same FMA semantics.
func mdctEncodeFMA32(a, b, c float32) float32 { return mdctFMA32(a, b, c) }
