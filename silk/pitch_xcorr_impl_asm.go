//go:build arm64

package silk

//go:noescape
func celtPitchXcorrFloatImpl(x, y []float32, out []float32, length, maxPitch int)
