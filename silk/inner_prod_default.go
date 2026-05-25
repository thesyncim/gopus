//go:build !arm64 || purego

package silk

// innerProductF32 computes the inner product of float32 signals using libopus ordering.
// Accumulates with the same C double precision as silk_inner_prod_aligned.
func innerProductF32(a, b []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	a = a[:length:length]
	b = b[:length:length]
	var s0, s1, s2, s3 silkCReal
	i := 0
	n := len(a) - 3
	for ; i < n; i += 4 {
		s0 += silkCReal(a[i]) * silkCReal(b[i])
		s1 += silkCReal(a[i+1]) * silkCReal(b[i+1])
		s2 += silkCReal(a[i+2]) * silkCReal(b[i+2])
		s3 += silkCReal(a[i+3]) * silkCReal(b[i+3])
	}
	for ; i < len(a); i++ {
		s0 += silkCReal(a[i]) * silkCReal(b[i])
	}
	return s0 + s1 + s2 + s3
}

// energyF32 computes energy (sum of squares) of a float32 signal.
// Accumulates with the same C double precision as silk_energy_FLP.
func energyF32(x []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	x = x[:length:length]
	var s0, s1, s2, s3 silkCReal
	i := 0
	n := len(x) - 3
	for ; i < n; i += 4 {
		d0, d1, d2, d3 := silkCReal(x[i]), silkCReal(x[i+1]), silkCReal(x[i+2]), silkCReal(x[i+3])
		s0 += d0 * d0
		s1 += d1 * d1
		s2 += d2 * d2
		s3 += d3 * d3
	}
	for ; i < len(x); i++ {
		d := silkCReal(x[i])
		s0 += d * d
	}
	return s0 + s1 + s2 + s3
}
