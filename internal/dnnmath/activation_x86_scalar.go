//go:build !amd64 || !goexperiment.simd || nosimd

package dnnmath

const dnnX86Enabled = false

func Cgemv8x4QuantizeInputX86(x float32) uint8 {
	return uint8(int32(Cgemv8x4QuantizeInputScalar(x)) + 127)
}

func sigmoidVectorX86(out, in []float32, n int) { SigmoidVectorScalarApprox(out, in, n) }
func tanhVectorX86(out, in []float32, n int)    { TanhVectorScalarApprox(out, in, n) }
func tanhApproxX86(x float32) float32           { return TanhScalarApprox(x) }
func expVectorX86(out, in []float32, n int)     { ExpVectorScalarApprox(out, in, n) }
