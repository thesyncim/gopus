//go:build !amd64 || !goexperiment.simd || nosimd

package rdovae

var rdovaeX86Enabled = false

func sgemvX86Fused(out []float32, weights FloatTensor, rows, cols, colStride int, x []float32) {
	sgemvSplit(out, weights, rows, cols, colStride, x)
}

func sparseSGEMVX86Fused(out []float32, weights FloatTensor, idx IntTensor, x []float32) {
	sparseSGEMV(out, weights, idx, x)
}
