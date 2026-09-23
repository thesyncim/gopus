//go:build !arm64

package celt

import "math"

func kissMulAddSource(a, b, c, d float32) (float32, bool) {
	ab := round32(a * b)
	cd := round32(c * d)
	result := ab + cd
	return result, kissMulSourceIsNaN(result)
}

func kissMulSubSource(a, b, c, d float32) (float32, bool) {
	ab := round32(a * b)
	cd := round32(c * d)
	result := ab - cd
	return result, kissMulSourceIsNaN(result)
}

func kissMulSourceIsNaN(x float32) bool {
	const exponent = uint32(0x7f800000)
	return math.Float32bits(x)&0x7fffffff > exponent
}

//go:noinline
func kissMulAddSourceNonFinite(a, b, c, d float32) float32 {
	return a*b + c*d
}

//go:noinline
func kissMulSubSourceNonFinite(a, b, c, d float32) float32 {
	return a*b - c*d
}
