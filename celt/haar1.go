package celt

// haar1Stride1Asm applies the Haar butterfly to n0 consecutive pairs of float64
// values. Computation is done in float32 precision (matching libopus) then
// widened back to float64 for storage. This preserves bit-exact parity with
// libopus which uses float intermediates.
func haar1Stride1Asm(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 1 + (n0-1)*2
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		tmp1 := invSqrt2 * float32(x[idx])
		tmp2 := invSqrt2 * float32(x[idx+1])
		x[idx] = float64(tmp1 + tmp2)
		x[idx+1] = float64(tmp1 - tmp2)
		idx += 2
	}
}
