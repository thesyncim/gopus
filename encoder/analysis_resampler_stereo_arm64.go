//go:build arm64

package encoder

//go:noescape
func silkResamplerDown2HPStereoImpl(state, out, in []float32, scale float32, n int) float32

func silkResamplerDown2HPStereo(s []float32, out []float32, in []float32, scale float32) float32 {
	len2 := len(in) / 4
	if len(out) < len2 {
		len2 = len(out)
	}
	if len2 <= 0 {
		return 0
	}
	return silkResamplerDown2HPStereoImpl(s, out[:len2], in[:4*len2], scale, len2)
}
