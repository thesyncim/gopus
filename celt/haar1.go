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

func haar1Stride2(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 3 + (n0-1)*4
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		a0 := invSqrt2 * float32(x[idx])
		b0 := invSqrt2 * float32(x[idx+2])
		x[idx] = float64(a0 + b0)
		x[idx+2] = float64(a0 - b0)

		a1 := invSqrt2 * float32(x[idx+1])
		b1 := invSqrt2 * float32(x[idx+3])
		x[idx+1] = float64(a1 + b1)
		x[idx+3] = float64(a1 - b1)
		idx += 4
	}
}

func haar1Stride4(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 7 + (n0-1)*8
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		a0 := invSqrt2 * float32(x[idx])
		b0 := invSqrt2 * float32(x[idx+4])
		x[idx] = float64(a0 + b0)
		x[idx+4] = float64(a0 - b0)

		a1 := invSqrt2 * float32(x[idx+1])
		b1 := invSqrt2 * float32(x[idx+5])
		x[idx+1] = float64(a1 + b1)
		x[idx+5] = float64(a1 - b1)

		a2 := invSqrt2 * float32(x[idx+2])
		b2 := invSqrt2 * float32(x[idx+6])
		x[idx+2] = float64(a2 + b2)
		x[idx+6] = float64(a2 - b2)

		a3 := invSqrt2 * float32(x[idx+3])
		b3 := invSqrt2 * float32(x[idx+7])
		x[idx+3] = float64(a3 + b3)
		x[idx+7] = float64(a3 - b3)
		idx += 8
	}
}
