package encoder

import "testing"

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
