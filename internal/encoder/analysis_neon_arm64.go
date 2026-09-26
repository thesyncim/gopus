//go:build arm64 && goexperiment.simd && !nosimd

package encoder

// analysisNEONReductions selects the arithmetic order of the NEON libopus build
// that the arm64 Go SIMD build pairs with. clang vectorizes the per-band bin
// loop of tonality_analysis (src/analysis.c) sixteen bins at a time with
// in-order reductions, so the tonality and noisiness products of those bins
// are rounded before they are added; the remaining bins keep the scalar fused
// multiply-add.
const analysisNEONReductions = true
