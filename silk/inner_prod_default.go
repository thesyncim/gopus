//go:build !arm64 && !amd64

package silk

// innerProductF32 computes the inner product of float32 signals using libopus ordering.
func innerProductF32(a, b []float32, length int) float64 {
	if length <= 0 {
		return 0
	}
	_ = a[length-1] // BCE hint
	_ = b[length-1] // BCE hint
	var result float64
	i := 0
	for i < length-3 {
		result += float64(a[i+0])*float64(b[i+0]) +
			float64(a[i+1])*float64(b[i+1]) +
			float64(a[i+2])*float64(b[i+2]) +
			float64(a[i+3])*float64(b[i+3])
		i += 4
	}
	for ; i < length; i++ {
		result += float64(a[i]) * float64(b[i])
	}
	return result
}

// innerProductFLP computes inner product of two float32 arrays.
// Matches libopus silk_inner_product_FLP (float precision accumulation).
func innerProductFLP(a, b []float32, length int) float64 {
	if length <= 0 {
		return 0
	}
	_ = a[length-1] // BCE hint
	_ = b[length-1] // BCE hint

	var result float64
	i := 0
	for ; i < length-3; i += 4 {
		result += float64(a[i+0])*float64(b[i+0]) +
			float64(a[i+1])*float64(b[i+1]) +
			float64(a[i+2])*float64(b[i+2]) +
			float64(a[i+3])*float64(b[i+3])
	}
	for ; i < length; i++ {
		result += float64(a[i]) * float64(b[i])
	}
	return result
}

// energyF32 computes energy of a float32 signal using libopus-style unrolling.
func energyF32(x []float32, length int) float64 {
	var energy float64
	i := 0
	for i < length-3 {
		d0 := float64(x[i+0])
		d1 := float64(x[i+1])
		d2 := float64(x[i+2])
		d3 := float64(x[i+3])
		energy += d0*d0 + d1*d1 + d2*d2 + d3*d3
		i += 4
	}
	for ; i < length; i++ {
		d := float64(x[i])
		energy += d * d
	}
	return energy
}
