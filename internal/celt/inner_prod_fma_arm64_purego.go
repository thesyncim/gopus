//go:build arm64 && purego

package celt

// celtInnerProd8FMA32 is the arm64 purego inner-product kernel. It reproduces
// the 4-lane accumulation order of celtInnerProdNeonStyle and the single-rounding
// FMA the NEON asm path emits, but reaches it through fma32 (a*b+c) rather than
// the portable math.FMA. On arm64 the backend contracts a*b+c into one FMADDS,
// which is bit-identical to float32(math.FMA(a,b,c)) for float32 inputs (the
// f64 round-trip is double-rounding-safe) while avoiding its FCVT round-trips.
// The libopus-oracle parity suite (TestCeltInnerProd8FMA32MatchesReference and
// the CELT synthesis-stage / stereo-merge tests) gates the contraction.
func celtInnerProd8FMA32(x, y []float32, n int) float32 {
	var acc [4]float32
	i := 0
	for ; i < n-7; i += 8 {
		for lane := 0; lane < 4; lane++ {
			acc[lane] = fma32(x[i+lane], y[i+lane], acc[lane])
		}
		for lane := 0; lane < 4; lane++ {
			acc[lane] = fma32(x[i+4+lane], y[i+4+lane], acc[lane])
		}
	}
	if n-i >= 4 {
		for lane := 0; lane < 4; lane++ {
			acc[lane] = fma32(x[i+lane], y[i+lane], acc[lane])
		}
		i += 4
	}
	sum0 := round32(acc[0] + acc[2])
	sum1 := round32(acc[1] + acc[3])
	sum := round32(sum0 + sum1)
	for ; i < n; i++ {
		sum = fma32(x[i], y[i], sum)
	}
	return sum
}
