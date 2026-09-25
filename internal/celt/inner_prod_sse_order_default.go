//go:build !amd64 || !goexperiment.simd || nosimd

package celt

func innerProdFloat32SSEOrder(x, y []float32, length int) float32 {
	return innerProdFloat32SSEOrderScalar(x, y, length)
}

func prefilterDualInnerProdF32SSEOrder(x, y1, y2 []float32, length int) (float32, float32) {
	return prefilterDualInnerProdF32SSEOrderScalar(x, y1, y2, length)
}
