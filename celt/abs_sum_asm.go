//go:build arm64 && !purego && gopus_legacy_float64_asm

package celt

//go:noescape
func absSum(x []float64) float64
