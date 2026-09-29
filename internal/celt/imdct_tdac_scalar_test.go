//go:build !arm64 || nosimd || purego || !goexperiment.simd

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestIMDCTTDACWindowScalarRandomMatchesReference checks the scalar TDAC
// windowing against imdctTDACWindowScalarRef on random data, for separate
// and aliased (xsrc == out) source buffers.
func TestIMDCTTDACWindowScalarRandomMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x7dac))
	randF32 := func() float32 {
		return float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(9)-4)))
	}
	for iter := range 400 {
		overlap := 2 * (1 + rng.Intn(120))
		count := overlap / 2
		window := make([]float32, overlap)
		for i := range window {
			window[i] = randF32()
		}
		blockStart := rng.Intn(8)
		n := blockStart + overlap + rng.Intn(8)
		base := make([]float32, n)
		for i := range base {
			base[i] = randF32()
		}
		src := make([]float32, n)
		for i := range src {
			src[i] = randF32()
		}
		yOut0 := blockStart
		xOut0 := blockStart + overlap - 1
		xSrc0 := count + rng.Intn(n-count)

		got := append([]float32(nil), base...)
		want := append([]float32(nil), base...)
		imdctTDACWindowScalar(got, src, window, yOut0, xOut0, xSrc0, overlap-1, count)
		imdctTDACWindowScalarRef(want, src, window, yOut0, xOut0, xSrc0, overlap-1, count)
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("iter %d separate: out[%d]=%v want %v", iter, i, got[i], want[i])
			}
		}

		got = append([]float32(nil), base...)
		want = append([]float32(nil), base...)
		imdctTDACWindowScalar(got, got, window, yOut0, xOut0, xOut0, overlap-1, count)
		imdctTDACWindowScalarRef(want, want, window, yOut0, xOut0, xOut0, overlap-1, count)
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("iter %d aliased: out[%d]=%v want %v", iter, i, got[i], want[i])
			}
		}
	}
}
