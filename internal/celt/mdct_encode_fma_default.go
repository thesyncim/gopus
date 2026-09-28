//go:build !arm64 || !nosimd

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// mdctEncodeFMA32 uses the shared fused-multiply-add helper. The arm64 nosimd
// implementation uses the Go backend's float32 contraction directly.
func mdctEncodeFMA32(a, b, c float32) float32 { return opusmath.FMA32(a, b, c) }
