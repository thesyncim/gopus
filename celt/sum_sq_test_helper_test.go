package celt

func sumOfSquaresF64toF32(x []float64, n int) float64 {
	if n <= 0 {
		return 0
	}
	sum := float32(0)
	for i := 0; i < n; i++ {
		v := float32(x[i])
		sum += v * v
	}
	return float64(sum)
}
