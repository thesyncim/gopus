//go:build arm64 && !purego && gopus_legacy_float64_asm

package celt

//go:noescape
func scaleFloat64Into(dst, src []float64, scale float64, n int)
