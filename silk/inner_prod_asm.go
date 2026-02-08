//go:build arm64 || amd64

package silk

// innerProductF32Asm computes the inner product of two float32 slices,
// accumulating in float64 precision. Uses 4 FMA accumulators to break
// dependency chains.
//
//go:noescape
func innerProductF32Asm(a, b []float32, length int) float64

// energyF32Asm computes the energy (sum of squares) of a float32 slice,
// accumulating in float64 precision. Equivalent to innerProductF32Asm(x, x, length)
// but avoids double-loading.
//
//go:noescape
func energyF32Asm(x []float32, length int) float64

// innerProductF32 computes the inner product of float32 signals using libopus ordering.
func innerProductF32(a, b []float32, length int) float64 {
	if length <= 0 {
		return 0
	}
	return innerProductF32Asm(a, b, length)
}

// innerProductFLP computes inner product of two float32 arrays.
// Matches libopus silk_inner_product_FLP (float precision accumulation).
func innerProductFLP(a, b []float32, length int) float64 {
	if length <= 0 {
		return 0
	}
	return innerProductF32Asm(a, b, length)
}

// energyF32 computes energy of a float32 signal using platform-specific FMA instructions.
func energyF32(x []float32, length int) float64 {
	if length <= 0 {
		return 0
	}
	return energyF32Asm(x, length)
}
