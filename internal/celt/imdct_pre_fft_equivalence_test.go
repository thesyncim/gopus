package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestIMDCTPreRotateFFTMatchesSplitPath checks the lane's pre-rotation + FFT
// against pre-rotating into fftIn and bit-reversing it into the FFT buffer.
func TestIMDCTPreRotateFFTMatchesSplitPath(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1dc7))
	for _, n := range []int{240, 480, 960, 1920} {
		n2, n4 := n/2, n/4
		trig := getMDCTTrigF32(n)
		st := getKissFFTState(n4)
		for iter := 0; iter < 20; iter++ {
			spectrum := make([]float32, n2)
			for i := range spectrum {
				spectrum[i] = float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(8))))
			}
			fftIn := make([]complex64, n4)
			got := imdctPreRotateFFT(fftIn, make([]kissCpx, n4), spectrum, trig, n2, n4, st)

			wantIn := make([]complex64, n4)
			imdctPreRotateF32Spectrum(wantIn, spectrum, trig, n2, n4)
			want := kissFFT32ToScratch(wantIn, make([]kissCpx, n4), st)
			for i := range want {
				if math.Float32bits(got[i].r) != math.Float32bits(want[i].r) || math.Float32bits(got[i].i) != math.Float32bits(want[i].i) {
					t.Fatalf("n=%d iter %d: out[%d]=%v want %v", n, iter, i, got[i], want[i])
				}
			}
		}
	}
}

// TestIMDCTPostRotateScalarMatchesReference checks the scalar post-rotation
// against the clt_mdct_backward_c() loop written with its yp0/yp1 cursors.
func TestIMDCTPostRotateScalarMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x9057))
	for _, n4 := range []int{1, 2, 3, 7, 30, 60, 120, 240, 480} {
		n2 := 2 * n4
		trig := make([]float32, n2)
		for i := range trig {
			trig[i] = float32(rng.Float64()*2 - 1)
		}
		fft := make([]kissCpx, n4)
		for i := range fft {
			fft[i] = kissCpx{float32(rng.NormFloat64() * 1e3), float32(rng.NormFloat64() * 1e3)}
		}
		got := make([]float32, n2)
		imdctPostRotateF32FromKissScalar(got, fft, trig, n2, n4)

		want := make([]float32, n2)
		yp0, yp1 := 0, n2-2
		for i := range (n4 + 1) >> 1 {
			k := n4 - 1 - i
			re, im := fft[i].i, fft[i].r
			t0, t1 := trig[i], trig[n4+i]
			yr := mdctMulAddMix(re, im, t0, t1)
			yi := mdctMulSubMix(re, im, t1, t0)
			re2, im2 := fft[k].i, fft[k].r
			want[yp0] = yr
			want[yp1+1] = yi
			t0, t1 = trig[n4-i-1], trig[n2-i-1]
			want[yp1] = mdctMulAddMix(re2, im2, t0, t1)
			want[yp0+1] = mdctMulSubMix(re2, im2, t1, t0)
			yp0 += 2
			yp1 -= 2
		}
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("n4=%d: buf[%d]=%v want %v", n4, i, got[i], want[i])
			}
		}
	}
}
