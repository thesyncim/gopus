package encoder

import "testing"

func silkResamplerDown2HPLegacy(s []float32, out []float32, in []float32) float32 {
	len2 := len(in) / 2
	if len(out) < len2 {
		len2 = len(out)
	}
	if len2 <= 0 {
		return 0
	}
	_ = in[2*len2-1]
	_ = out[len2-1]
	_ = s[2]

	s0, s1, s2 := s[0], s[1], s[2]
	const (
		coef0 = float32(0.6074371)
		coef1 = float32(0.15063)
	)

	var hpEner float64
	for k := 0; k < len2; k++ {
		in32 := in[2*k]
		y := in32 - s0
		xf := coef0 * y
		out32 := s0 + xf
		s0 = in32 + xf
		out32HP := out32

		in32 = in[2*k+1]
		y = in32 - s1
		xf = coef1 * y
		out32 = out32 + s1 + xf
		s1 = in32 + xf

		y = -in32 - s2
		xf = coef1 * y
		out32HP = out32HP + s2 + xf
		s2 = -in32 + xf

		hpEner += float64(out32HP * out32HP)
		out[k] = 0.5 * out32
	}

	s[0], s[1], s[2] = s0, s1, s2
	return float32(hpEner)
}

func benchmarkSilkResamplerDown2HP(b *testing.B, fn func([]float32, []float32, []float32) float32) {
	in := makeTonalityBenchPCM(960, 1)
	out := make([]float32, 480)
	state := []float32{0.11, -0.23, 0.37}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := append([]float32(nil), state...)
		fn(s, out, in)
	}
}

func BenchmarkSilkResamplerDown2HPLegacy(b *testing.B) {
	benchmarkSilkResamplerDown2HP(b, silkResamplerDown2HPLegacy)
}

func BenchmarkSilkResamplerDown2HPCurrent(b *testing.B) {
	benchmarkSilkResamplerDown2HP(b, silkResamplerDown2HP)
}
