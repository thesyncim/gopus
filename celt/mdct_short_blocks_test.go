package celt

import "testing"

func mdctShortBlocksCoreLegacy(samples []float64, overlap, shortBlocks, shortSize int, output, blockCoeffs []float64, blockMDCT func(block, coeffs []float64)) {
	for b := 0; b < shortBlocks; b++ {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}
		blockMDCT(samples[start:end], blockCoeffs)
		for i, v := range blockCoeffs {
			outIdx := b + i*shortBlocks
			if outIdx < len(output) {
				output[outIdx] = v
			}
		}
	}
}

func mdctShortBlocksStub(block, coeffs []float64) {
	base := float64(len(block))
	for i := range coeffs {
		coeffs[i] = base + float64(i)*0.25
	}
}

func TestMDCTShortBlocksCoreMatchesLegacy(t *testing.T) {
	samples := make([]float64, 1080)
	for i := range samples {
		samples[i] = float64((i%31)-15) * 0.125
	}
	const (
		overlap     = 120
		shortBlocks = 8
		shortSize   = 120
	)
	outputLen := shortBlocks * shortSize
	want := make([]float64, outputLen)
	got := make([]float64, outputLen)
	blockCoeffsWant := make([]float64, shortSize)
	blockCoeffsGot := make([]float64, shortSize)

	mdctShortBlocksCoreLegacy(samples, overlap, shortBlocks, shortSize, want, blockCoeffsWant, mdctShortBlocksStub)
	mdctShortBlocksCore(samples, overlap, shortBlocks, shortSize, got, blockCoeffsGot, mdctShortBlocksStub)

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("coefficient %d mismatch: got %.9g want %.9g", i, got[i], want[i])
		}
	}
}

func benchmarkMDCTShortBlocksCore(b *testing.B, legacy bool) {
	samples := make([]float64, 1080)
	for i := range samples {
		samples[i] = float64((i%31)-15) * 0.125
	}
	const (
		overlap     = 120
		shortBlocks = 8
		shortSize   = 120
	)
	output := make([]float64, shortBlocks*shortSize)
	blockCoeffs := make([]float64, shortSize)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if legacy {
			mdctShortBlocksCoreLegacy(samples, overlap, shortBlocks, shortSize, output, blockCoeffs, mdctShortBlocksStub)
		} else {
			mdctShortBlocksCore(samples, overlap, shortBlocks, shortSize, output, blockCoeffs, mdctShortBlocksStub)
		}
	}
}

func BenchmarkMDCTShortBlocksCoreCurrent(b *testing.B) {
	benchmarkMDCTShortBlocksCore(b, false)
}

func BenchmarkMDCTShortBlocksCoreLegacy(b *testing.B) {
	benchmarkMDCTShortBlocksCore(b, true)
}
