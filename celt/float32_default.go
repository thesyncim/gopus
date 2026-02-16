//go:build !arm64 && !amd64

package celt

// roundFloat64ToFloat32 rounds each element to float32 precision and back.
// This helps match libopus float-path precision.
func roundFloat64ToFloat32(x []float64) {
	for i, v := range x {
		x[i] = float64(float32(v))
	}
}
