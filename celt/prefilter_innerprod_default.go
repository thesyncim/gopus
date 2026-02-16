//go:build !arm64 && !amd64

package celt

// prefilterInnerProd uses float32 accumulation to match libopus float-path
// numerics in pitch/pre-filter analysis.
func prefilterInnerProd(x, y []float64, length int) float64 {
	if length <= 0 {
		return 0
	}
	_ = x[length-1]
	_ = y[length-1]
	sum := float32(0)
	for i := 0; i < length; i++ {
		sum += float32(x[i]) * float32(y[i])
	}
	return float64(sum)
}

// prefilterDualInnerProd computes two float32-accumulated dot products.
func prefilterDualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	if length <= 0 {
		return 0, 0
	}
	_ = x[length-1]
	_ = y1[length-1]
	_ = y2[length-1]
	sum1 := float32(0)
	sum2 := float32(0)
	for i := 0; i < length; i++ {
		xi := float32(x[i])
		sum1 += xi * float32(y1[i])
		sum2 += xi * float32(y2[i])
	}
	return float64(sum1), float64(sum2)
}
