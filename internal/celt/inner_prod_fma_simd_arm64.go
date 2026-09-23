//go:build arm64 && goexperiment.simd && !purego

package celt

// celtInnerProd8FMA32 is the archsimd dot product. arm64 NEON always provides the
// fused FMLA, so the 4-lane archsimd accumulator runs unconditionally and stays
// bit-exact with the scalar reference and the hand asm.
func celtInnerProd8FMA32(x, y []float32, n int) float32 {
	if n <= 0 {
		return 0
	}
	return innerProd8FMA32ArchSIMD(x, y, n)
}
