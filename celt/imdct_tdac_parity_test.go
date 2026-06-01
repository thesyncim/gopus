package celt

import (
	"math"
	"testing"
)

// imdctTDACWindowFMA32ScalarRef is an independent scalar reference for the
// FMA-like TDAC windowing. It rounds the standalone product to float32 and
// fuses the first multiply into the add/sub via math.FMA, matching the purego
// fallback and the arm64 assembly bit-for-bit.
func imdctTDACWindowFMA32ScalarRef(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int) {
	for i := 0; i < count; i++ {
		x1 := float64(xsrc[xSrc0-i])
		x2 := float64(out[yOut0+i])
		w1 := float64(window[i])
		w2 := float64(window[wBwd0-i])
		yOut := float32(math.FMA(x2, w2, -float64(float32(x1*w1))))
		xOut := float32(math.FMA(x2, w1, float64(float32(x1*w2))))
		out[yOut0+i] = yOut
		out[xOut0-i] = xOut
	}
}

func TestIMDCTTDACWindowFMA32MatchesScalar(t *testing.T) {
	for _, overlap := range []int{2, 4, 6, 8, 16, 30, 60, 120, 240} {
		count := overlap / 2
		window := make([]float32, overlap)
		for i := range window {
			window[i] = float32((i*3%53)-26) * 0.018
		}

		// In-place variant: out region [blockStart, blockStart+overlap),
		// x1 from a separate buf indexed by xp1-start.
		blockStart := 3
		start := blockStart + overlap/2
		n := blockStart + overlap + 5
		buf := make([]float32, n)
		for i := range buf {
			buf[i] = float32((i*7%89)-44) * 0.011
		}
		got := make([]float32, n)
		want := make([]float32, n)
		for i := range got {
			got[i] = float32((i*5%71)-35) * 0.009
			want[i] = got[i]
		}
		yp1 := blockStart
		xp1 := blockStart + overlap - 1
		imdctTDACWindowFMA32(got, buf, window, yp1, xp1, xp1-start, overlap-1, count)
		imdctTDACWindowFMA32ScalarRef(want, buf, window, yp1, xp1, xp1-start, overlap-1, count)
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("inplace overlap=%d idx=%d: got %v want %v", overlap, i, got[i], want[i])
			}
		}

		// In-buffer variant: xsrc == out, xSrc0 == xOut0.
		got2 := make([]float32, overlap)
		want2 := make([]float32, overlap)
		for i := range got2 {
			got2[i] = float32((i*11%67)-33) * 0.013
			want2[i] = got2[i]
		}
		imdctTDACWindowFMA32(got2, got2, window, 0, overlap-1, overlap-1, overlap-1, count)
		imdctTDACWindowFMA32ScalarRef(want2, want2, window, 0, overlap-1, overlap-1, overlap-1, count)
		for i := range want2 {
			if math.Float32bits(got2[i]) != math.Float32bits(want2[i]) {
				t.Fatalf("inbuf overlap=%d idx=%d: got %v want %v", overlap, i, got2[i], want2[i])
			}
		}
	}
}

func BenchmarkIMDCTTDACWindowFMA32(b *testing.B) {
	overlap := 120
	count := overlap / 2
	window := make([]float32, overlap)
	for i := range window {
		window[i] = float32((i*3%53)-26) * 0.018
	}
	out := make([]float32, overlap)
	for i := range out {
		out[i] = float32((i*5%71)-35) * 0.009
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctTDACWindowFMA32(out, out, window, 0, overlap-1, overlap-1, overlap-1, count)
	}
}
