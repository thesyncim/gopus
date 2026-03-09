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

func haar1Stride6(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 11 + (n0-1)*12
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		a0 := invSqrt2 * float32(x[idx])
		b0 := invSqrt2 * float32(x[idx+6])
		x[idx] = float64(a0 + b0)
		x[idx+6] = float64(a0 - b0)

		a1 := invSqrt2 * float32(x[idx+1])
		b1 := invSqrt2 * float32(x[idx+7])
		x[idx+1] = float64(a1 + b1)
		x[idx+7] = float64(a1 - b1)

		a2 := invSqrt2 * float32(x[idx+2])
		b2 := invSqrt2 * float32(x[idx+8])
		x[idx+2] = float64(a2 + b2)
		x[idx+8] = float64(a2 - b2)

		a3 := invSqrt2 * float32(x[idx+3])
		b3 := invSqrt2 * float32(x[idx+9])
		x[idx+3] = float64(a3 + b3)
		x[idx+9] = float64(a3 - b3)

		a4 := invSqrt2 * float32(x[idx+4])
		b4 := invSqrt2 * float32(x[idx+10])
		x[idx+4] = float64(a4 + b4)
		x[idx+10] = float64(a4 - b4)

		a5 := invSqrt2 * float32(x[idx+5])
		b5 := invSqrt2 * float32(x[idx+11])
		x[idx+5] = float64(a5 + b5)
		x[idx+11] = float64(a5 - b5)
		idx += 12
	}
}

func haar1Stride8(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 15 + (n0-1)*16
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		a0 := invSqrt2 * float32(x[idx])
		b0 := invSqrt2 * float32(x[idx+8])
		x[idx] = float64(a0 + b0)
		x[idx+8] = float64(a0 - b0)

		a1 := invSqrt2 * float32(x[idx+1])
		b1 := invSqrt2 * float32(x[idx+9])
		x[idx+1] = float64(a1 + b1)
		x[idx+9] = float64(a1 - b1)

		a2 := invSqrt2 * float32(x[idx+2])
		b2 := invSqrt2 * float32(x[idx+10])
		x[idx+2] = float64(a2 + b2)
		x[idx+10] = float64(a2 - b2)

		a3 := invSqrt2 * float32(x[idx+3])
		b3 := invSqrt2 * float32(x[idx+11])
		x[idx+3] = float64(a3 + b3)
		x[idx+11] = float64(a3 - b3)

		a4 := invSqrt2 * float32(x[idx+4])
		b4 := invSqrt2 * float32(x[idx+12])
		x[idx+4] = float64(a4 + b4)
		x[idx+12] = float64(a4 - b4)

		a5 := invSqrt2 * float32(x[idx+5])
		b5 := invSqrt2 * float32(x[idx+13])
		x[idx+5] = float64(a5 + b5)
		x[idx+13] = float64(a5 - b5)

		a6 := invSqrt2 * float32(x[idx+6])
		b6 := invSqrt2 * float32(x[idx+14])
		x[idx+6] = float64(a6 + b6)
		x[idx+14] = float64(a6 - b6)

		a7 := invSqrt2 * float32(x[idx+7])
		b7 := invSqrt2 * float32(x[idx+15])
		x[idx+7] = float64(a7 + b7)
		x[idx+15] = float64(a7 - b7)
		idx += 16
	}
}

func haar1Stride12(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 23 + (n0-1)*24
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		a0 := invSqrt2 * float32(x[idx])
		b0 := invSqrt2 * float32(x[idx+12])
		x[idx] = float64(a0 + b0)
		x[idx+12] = float64(a0 - b0)

		a1 := invSqrt2 * float32(x[idx+1])
		b1 := invSqrt2 * float32(x[idx+13])
		x[idx+1] = float64(a1 + b1)
		x[idx+13] = float64(a1 - b1)

		a2 := invSqrt2 * float32(x[idx+2])
		b2 := invSqrt2 * float32(x[idx+14])
		x[idx+2] = float64(a2 + b2)
		x[idx+14] = float64(a2 - b2)

		a3 := invSqrt2 * float32(x[idx+3])
		b3 := invSqrt2 * float32(x[idx+15])
		x[idx+3] = float64(a3 + b3)
		x[idx+15] = float64(a3 - b3)

		a4 := invSqrt2 * float32(x[idx+4])
		b4 := invSqrt2 * float32(x[idx+16])
		x[idx+4] = float64(a4 + b4)
		x[idx+16] = float64(a4 - b4)

		a5 := invSqrt2 * float32(x[idx+5])
		b5 := invSqrt2 * float32(x[idx+17])
		x[idx+5] = float64(a5 + b5)
		x[idx+17] = float64(a5 - b5)

		a6 := invSqrt2 * float32(x[idx+6])
		b6 := invSqrt2 * float32(x[idx+18])
		x[idx+6] = float64(a6 + b6)
		x[idx+18] = float64(a6 - b6)

		a7 := invSqrt2 * float32(x[idx+7])
		b7 := invSqrt2 * float32(x[idx+19])
		x[idx+7] = float64(a7 + b7)
		x[idx+19] = float64(a7 - b7)

		a8 := invSqrt2 * float32(x[idx+8])
		b8 := invSqrt2 * float32(x[idx+20])
		x[idx+8] = float64(a8 + b8)
		x[idx+20] = float64(a8 - b8)

		a9 := invSqrt2 * float32(x[idx+9])
		b9 := invSqrt2 * float32(x[idx+21])
		x[idx+9] = float64(a9 + b9)
		x[idx+21] = float64(a9 - b9)

		a10 := invSqrt2 * float32(x[idx+10])
		b10 := invSqrt2 * float32(x[idx+22])
		x[idx+10] = float64(a10 + b10)
		x[idx+22] = float64(a10 - b10)

		a11 := invSqrt2 * float32(x[idx+11])
		b11 := invSqrt2 * float32(x[idx+23])
		x[idx+11] = float64(a11 + b11)
		x[idx+23] = float64(a11 - b11)
		idx += 24
	}
}
