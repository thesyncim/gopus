//go:build (!amd64 && !arm64) || !goexperiment.simd || nosimd || purego

package encoder

// analysisBinsSIMD leaves every bin to the scalar loop on builds without the
// amd64 SIMD kernel.
func (s *TonalityAnalysisState) analysisBinsSIMD(out *[480]complex64, tonality, tonality2, noisiness []float32) int {
	return 1
}

func smoothAnalysisTonality(tonality, tonality2 []float32) {
	tonality = tonality[:240:240]
	tonality2 = tonality2[:240:240]
	for i := 2; i < 239; i++ {
		tt := minf(tonality2[i], maxf(tonality2[i-1], tonality2[i+1]))
		tonality[i] = 0.9 * maxf(tonality[i], tt-0.1)
	}
}
