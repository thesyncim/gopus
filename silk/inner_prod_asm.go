//go:build arm64

package silk

//go:noescape
func innerProductF32(a, b []float32, length int) float64

//go:noescape
func innerProductFLP(a, b []float32, length int) float64

//go:noescape
func energyF32(x []float32, length int) float64
