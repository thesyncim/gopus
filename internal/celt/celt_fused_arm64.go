//go:build arm64 && goexperiment.simd && !nosimd

package celt

// celtFusedFloat selects the arithmetic order of the NEON libopus build that the
// arm64 Go SIMD build pairs with: the NEON intrinsic kernels (for example
// celt_inner_prod_neon) and clang's vectorized reductions
// (neonRoundsReductionTerm). The transient high-pass form it also selects is
// not yet bit-exact with that build.
const celtFusedFloat = true
