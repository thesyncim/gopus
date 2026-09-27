//go:build !amd64 || !goexperiment.simd || nosimd

package rdovae

var rdovaeX86Enabled = false

func sgemvX86Fused(out []float32, weights FloatTensor, rows, cols, colStride int, x []float32) {
	sgemvSplit(out, weights, rows, cols, colStride, x)
}
