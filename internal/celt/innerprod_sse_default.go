//go:build !amd64 || nosimd || !goexperiment.simd

package celt

func celtInnerProdSSEStyleImpl(x, y []celtNorm) float32 {
	return celtInnerProdSSEStyleGo(x, y)
}
