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
