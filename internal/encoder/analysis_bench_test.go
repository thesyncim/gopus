package encoder

import (
	"math"
	"testing"
)

func makeTonalityBenchPCM(frameSize, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		t := float64(i)
		base := 0.32*math.Sin(2*math.Pi*440*t/48000.0) +
			0.21*math.Sin(2*math.Pi*880*t/48000.0+0.17) +
			0.09*math.Sin(2*math.Pi*1760*t/48000.0+0.43)
		if channels == 2 {
			pcm[2*i] = float32(base)
			pcm[2*i+1] = float32(0.94*base + 0.03*math.Sin(2*math.Pi*330*t/48000.0+0.29))
			continue
		}
		pcm[i] = float32(base)
	}
	return pcm
}

func benchmarkTonalityAnalysis48k(b *testing.B, channels int) {
	const frameSize = 960
	pcm := makeTonalityBenchPCM(frameSize, channels)
	s := NewTonalityAnalysisState(48000)

	// Enter the steady-state path before measuring.
	for i := 0; i < 8; i++ {
		s.tonalityAnalysis(pcm, channels)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.tonalityAnalysis(pcm, channels)
	}
}

func BenchmarkTonalityAnalysis48kMono(b *testing.B) {
	benchmarkTonalityAnalysis48k(b, 1)
}

func BenchmarkTonalityAnalysis48kStereo(b *testing.B) {
	benchmarkTonalityAnalysis48k(b, 2)
}

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

var analysisMLPBenchSink [MaxNeurons]float32

func benchmarkGemmAccumF32(b *testing.B, rows, cols, stride int, weights []float32, input []float32) {
	out := make([]float32, rows)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range out {
			out[j] = 0
		}
		gemmAccumF32(out, weights, rows, cols, stride, input)
	}
	copy(analysisMLPBenchSink[:], out)
}

func BenchmarkGemmAccumF32Rows24(b *testing.B) {
	input := make([]float32, layer1.NbInputs)
	for i := range input {
		input[i] = 0.02 * float32(i+1)
	}
	benchmarkGemmAccumF32(b, layer1.NbNeurons, layer1.NbInputs, 3*layer1.NbNeurons, layer1.inputWeightsF32, input)
}

func BenchmarkGemmAccumF32Rows32(b *testing.B) {
	input := make([]float32, layer0.NbInputs)
	for i := range input {
		input[i] = 0.015 * float32(i+1)
	}
	benchmarkGemmAccumF32(b, layer0.NbNeurons, layer0.NbInputs, layer0.NbNeurons, layer0.inputWeightsF32, input)
}

func BenchmarkAnalysisGRU(b *testing.B) {
	var state [MaxNeurons]float32
	input := make([]float32, layer1.NbInputs)
	for i := range input {
		input[i] = 0.01 * float32(i+1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		layer1.ComputeGRU(state[:], input)
	}
	copy(analysisMLPBenchSink[:], state[:])
}
