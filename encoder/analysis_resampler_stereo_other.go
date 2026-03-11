//go:build !arm64

package encoder

func silkResamplerDown2HPStereo(s []float32, out []float32, in []float32, scale float32) float32 {
	return silkResamplerDown2HPStereoGeneric(s, out, in, scale)
}
