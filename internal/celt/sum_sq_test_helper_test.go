package celt

func sumOfSquaresF64toF32(x []float64, n int) float64 {
	if n <= 0 {
		return 0
	}
	if n > len(x) {
		n = len(x)
	}
	tmp := make([]float32, n)
	for i := range tmp {
		tmp[i] = float32(x[i])
	}
	return float64(celtInnerProdF32LibopusOrder(tmp))
}
