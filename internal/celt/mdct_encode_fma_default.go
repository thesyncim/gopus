//go:build !arm64 || !nosimd

package celt

import "github.com/thesyncim/gopus/internal/opusmath"

// mdctEncodeFMA32 delegates to opusmath.FMA32 on every target where the encoder
// and the IMDCT decoder share the same fused-multiply-add semantics; only
// arm64 nosimd diverges (see the sibling _arm64_nosimd file).
func mdctEncodeFMA32(a, b, c float32) float32 { return opusmath.FMA32(a, b, c) }
