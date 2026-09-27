//go:build !amd64 || !goexperiment.simd || nosimd

package celt

const combUsesSSE = false

// combFilterConstSSE is only reached when combUsesSSE is true; builds without
// the amd64 SIMD kernel keep the scalar form of the same operation order.
func combFilterConstSSE(dst, src, delay []celtSig, from, to int, g10, g11, g12 float32) {
	for i := from; i < to; i++ {
		dst[i] = combFilterConstSSEValue(src[i], g10, g11, g12, delay[i+2], delay[i+3], delay[i+1], delay[i+4], delay[i])
	}
}
