//go:build arm64 && !purego

package celt

// sumOfSquaresF64toF32 converts float64 elements to float32 and accumulates
// the sum of squares with the same lane order as libopus' arm64 NEON
// celt_inner_prod_neon() path.
func sumOfSquaresF64toF32(x []float64, n int) float64 {
	if n <= 0 {
		return 0
	}

	var xy0, xy1, xy2, xy3 float32
	i := 0
	for ; i < n-7; i += 8 {
		v0 := float32(x[i+0])
		v1 := float32(x[i+1])
		v2 := float32(x[i+2])
		v3 := float32(x[i+3])
		xy0 += v0 * v0
		xy1 += v1 * v1
		xy2 += v2 * v2
		xy3 += v3 * v3

		v4 := float32(x[i+4])
		v5 := float32(x[i+5])
		v6 := float32(x[i+6])
		v7 := float32(x[i+7])
		xy0 += v4 * v4
		xy1 += v5 * v5
		xy2 += v6 * v6
		xy3 += v7 * v7
	}

	if n-i >= 4 {
		v0 := float32(x[i+0])
		v1 := float32(x[i+1])
		v2 := float32(x[i+2])
		v3 := float32(x[i+3])
		xy0 += v0 * v0
		xy1 += v1 * v1
		xy2 += v2 * v2
		xy3 += v3 * v3
		i += 4
	}

	xy := (xy0 + xy2) + (xy1 + xy3)
	for ; i < n; i++ {
		v := float32(x[i])
		xy += v * v
	}
	return float64(xy)
}
