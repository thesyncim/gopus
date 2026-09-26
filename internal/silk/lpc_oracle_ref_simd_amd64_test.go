//go:build amd64 && goexperiment.simd && !nosimd

package silk

func silkLPCOracleUsesAVX2() bool {
	return silkUseInnerProductFLPAVX2FMA
}
