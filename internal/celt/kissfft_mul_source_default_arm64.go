//go:build arm64 && !nosimd

package celt

func kissMulAddSource(a, b, c, d float32) (float32, bool) {
	return fma32(a, b, noFMA32Mul(c, d)), false
}

func kissMulSubSource(a, b, c, d float32) (float32, bool) {
	return fma32(a, b, -noFMA32Mul(c, d)), false
}

func kissMulAddSourceNonFinite(a, b, c, d float32) float32 {
	value, _ := kissMulAddSource(a, b, c, d)
	return value
}

func kissMulSubSourceNonFinite(a, b, c, d float32) float32 {
	value, _ := kissMulSubSource(a, b, c, d)
	return value
}
