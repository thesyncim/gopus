//go:build arm64 && !purego

package silk

//go:noescape
func innerProductF32(a, b []float32, length int) silkCReal

//go:noescape
func energyF32(x []float32, length int) silkCReal
