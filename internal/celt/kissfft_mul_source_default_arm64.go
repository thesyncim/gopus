//go:build arm64 && !purego

package celt

func kissMulAddSource(a, b, c, d float32) float32 {
	return fma32(a, b, noFMA32Mul(c, d))
}

func kissMulSubSource(a, b, c, d float32) float32 {
	return fma32(a, b, -noFMA32Mul(c, d))
}
