package celt

import (
	"math"
	"testing"
)

// imdctPreRotateFMA32KissScalarRef is an independent scalar reference for the
// FMA-like IMDCT pre-rotation. It rounds the standalone product to float32 and
// fuses the first multiply into the add via math.FMA, matching both the purego
// fallback and the arm64 assembly bit-for-bit.
func imdctPreRotateFMA32KissScalarRef(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	for i := 0; i < n4; i++ {
		x1 := float64(spectrum[2*i])
		x2 := float64(spectrum[n2-1-2*i])
		t0 := float64(trig[i])
		t1 := float64(trig[n4+i])
		prodR := float64(float32(x2 * t1))
		prodI := float64(float32(x1 * t1))
		yr := float32(math.FMA(x1, t0, -prodR))
		yi := float32(math.FMA(x2, t0, prodI))
		fftIn[i] = complex(yr, yi)
	}
}

func TestIMDCTPreRotateFMA32KissMatchesScalar(t *testing.T) {
	for _, n2 := range []int{2, 4, 10, 60, 120, 122, 240, 480} {
		n4 := n2 / 2
		if n4 == 0 {
			continue
		}
		spectrum := make([]float32, n2)
		trig := make([]float32, n2)
		for i := range spectrum {
			spectrum[i] = float32((i*7%101)-50) * 0.013
		}
		for i := range trig {
			trig[i] = float32((i*5%97)-48) * 0.021
		}

		got := make([]complex64, n4)
		want := make([]complex64, n4)
		imdctPreRotateFMA32Kiss(got, spectrum, trig, n2, n4)
		imdctPreRotateFMA32KissScalarRef(want, spectrum, trig, n2, n4)
		for i := range want {
			gr, gi := real(got[i]), imag(got[i])
			wr, wi := real(want[i]), imag(want[i])
			if math.Float32bits(gr) != math.Float32bits(wr) || math.Float32bits(gi) != math.Float32bits(wi) {
				t.Fatalf("n2=%d i=%d: got (%v,%v) want (%v,%v)", n2, i, gr, gi, wr, wi)
			}
		}
	}
}

func BenchmarkIMDCTPreRotateFMA32Kiss(b *testing.B) {
	n2 := 120
	n4 := n2 / 2
	spectrum := make([]float32, n2)
	trig := make([]float32, n2)
	fftIn := make([]complex64, n4)
	for i := range spectrum {
		spectrum[i] = float32((i%17)-8) * 0.0625
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctPreRotateFMA32Kiss(fftIn, spectrum, trig, n2, n4)
	}
}
