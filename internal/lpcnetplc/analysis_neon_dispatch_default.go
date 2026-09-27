//go:build !arm64 || !goexperiment.simd || nosimd

package lpcnetplc

const useNEONAnalysisKernels = false
