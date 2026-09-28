//go:build !amd64 || nosimd || !goexperiment.simd

package celt

// stereoSplitInto runs stereo_split() over x and y of equal length.
func stereoSplitInto(x, y []celtNorm) {
	stereoSplitScalar(x, y)
}
