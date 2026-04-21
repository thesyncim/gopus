package silk

// innerProductF32Libopus matches libopus silk_inner_product_FLP_c() more
// closely than the generic helpers by using the same single-accumulator
// 4-sample update pattern.
func innerProductF32Libopus(a, b []float32, length int) float64 {
	if length <= 0 {
		return 0
	}
	a = a[:length:length]
	b = b[:length:length]
	result := 0.0
	i := 0
	for ; i < length-3; i += 4 {
		result += float64(a[i])*float64(b[i]) +
			float64(a[i+1])*float64(b[i+1]) +
			float64(a[i+2])*float64(b[i+2]) +
			float64(a[i+3])*float64(b[i+3])
	}
	for ; i < length; i++ {
		result += float64(a[i]) * float64(b[i])
	}
	return result
}

// energyF32Libopus matches libopus silk_energy_FLP() using the same
// single-accumulator chunking.
func energyF32Libopus(x []float32, length int) float64 {
	if length <= 0 {
		return 0
	}
	x = x[:length:length]
	result := 0.0
	i := 0
	for ; i < length-3; i += 4 {
		result += float64(x[i])*float64(x[i]) +
			float64(x[i+1])*float64(x[i+1]) +
			float64(x[i+2])*float64(x[i+2]) +
			float64(x[i+3])*float64(x[i+3])
	}
	for ; i < length; i++ {
		result += float64(x[i]) * float64(x[i])
	}
	return result
}
