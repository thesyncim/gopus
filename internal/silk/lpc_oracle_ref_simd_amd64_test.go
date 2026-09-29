//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

func silkLPCOracleUsesAVX2() bool {
	return silkUseInnerProductFLPAVX2FMA
}
