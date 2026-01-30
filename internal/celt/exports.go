package celt

// ExportedOpPVQSearch exposes opPVQSearch for testing.
func ExportedOpPVQSearch(x []float64, k int) ([]int, float64) {
	return opPVQSearch(x, k)
}
