package silk

// innerProductF32 computes the inner product of float32 signals using libopus ordering.
// Accumulates in float64 precision to match libopus silk_inner_prod_aligned.
func innerProductF32(a, b []float32, length int) float64 {
	if length <= 0 {
		return 0
	}
	a = a[:length:length]
	b = b[:length:length]
	var s0, s1, s2, s3 float64
	i := 0
	n := len(a) - 3
	for ; i < n; i += 4 {
		s0 += float64(a[i]) * float64(b[i])
		s1 += float64(a[i+1]) * float64(b[i+1])
		s2 += float64(a[i+2]) * float64(b[i+2])
		s3 += float64(a[i+3]) * float64(b[i+3])
	}
	for ; i < len(a); i++ {
		s0 += float64(a[i]) * float64(b[i])
	}
	return s0 + s1 + s2 + s3
}

// innerProductFLP computes inner product of two float32 arrays.
// Matches libopus silk_inner_product_FLP (float precision accumulation).
func innerProductFLP(a, b []float32, length int) float64 {
	if length <= 0 {
		return 0
	}
	a = a[:length:length]
	b = b[:length:length]
	var s0, s1, s2, s3 float64
	i := 0
	n := len(a) - 3
	for ; i < n; i += 4 {
		s0 += float64(a[i]) * float64(b[i])
		s1 += float64(a[i+1]) * float64(b[i+1])
		s2 += float64(a[i+2]) * float64(b[i+2])
		s3 += float64(a[i+3]) * float64(b[i+3])
	}
	for ; i < len(a); i++ {
		s0 += float64(a[i]) * float64(b[i])
	}
	return s0 + s1 + s2 + s3
}

// energyF32 computes energy (sum of squares) of a float32 signal.
// Accumulates in float64 precision.
func energyF32(x []float32, length int) float64 {
	if length <= 0 {
		return 0
	}
	x = x[:length:length]
	var s0, s1, s2, s3 float64
	i := 0
	n := len(x) - 3
	for ; i < n; i += 4 {
		d0, d1, d2, d3 := float64(x[i]), float64(x[i+1]), float64(x[i+2]), float64(x[i+3])
		s0 += d0 * d0
		s1 += d1 * d1
		s2 += d2 * d2
		s3 += d3 * d3
	}
	for ; i < len(x); i++ {
		d := float64(x[i])
		s0 += d * d
	}
	return s0 + s1 + s2 + s3
}
