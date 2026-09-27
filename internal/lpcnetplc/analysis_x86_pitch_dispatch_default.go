//go:build !amd64 || !goexperiment.simd || nosimd

package lpcnetplc

const useX86SelectedPitchKernels = false
