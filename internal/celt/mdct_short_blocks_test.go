package celt

import "testing"

func mdctShortBlocksCoreLegacy(samples []float32, overlap, shortBlocks, shortSize int, output, blockCoeffs []float32, blockMDCT func(block, coeffs []float32)) {
	for b := range shortBlocks {
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

func mdctShortBlocksStub(block, coeffs []float32) {
	base := float32(len(block))
	for i := range coeffs {
		coeffs[i] = base + float32(i)*0.25
	}
}

func TestMDCTShortBlocksCoreMatchesLegacy(t *testing.T) {
	samples := make([]float32, 1080)
	for i := range samples {
		samples[i] = float32((i%31)-15) * 0.125
	}
	const (
		overlap     = 120
		shortBlocks = 8
		shortSize   = 120
	)
	outputLen := shortBlocks * shortSize
	want := make([]float32, outputLen)
	got := make([]float32, outputLen)
	blockCoeffsWant := make([]float32, shortSize)
	blockCoeffsGot := make([]float32, shortSize)

	mdctShortBlocksCoreLegacy(samples, overlap, shortBlocks, shortSize, want, blockCoeffsWant, mdctShortBlocksStub)
	mdctShortBlocksCore(samples, overlap, shortBlocks, shortSize, got, blockCoeffsGot, mdctShortBlocksStub)

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("coefficient %d mismatch: got %.9g want %.9g", i, got[i], want[i])
		}
	}
}

func benchmarkMDCTShortBlocksCore(b *testing.B, legacy bool) {
	samples := make([]float32, 1080)
	for i := range samples {
		samples[i] = float32((i%31)-15) * 0.125
	}
	const (
		overlap     = 120
		shortBlocks = 8
		shortSize   = 120
	)
	output := make([]float32, shortBlocks*shortSize)
	blockCoeffs := make([]float32, shortSize)

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
