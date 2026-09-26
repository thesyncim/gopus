//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestIMDCTRotateAMD64MatchesScalar pins the archsimd pre/post rotations to
// their scalar references bit-for-bit over every CELT IMDCT size plus odd
// quarter lengths that exercise the scalar tails.
func TestIMDCTRotateAMD64MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	f := func() float32 {
		if rng.Intn(16) == 0 {
			return float32(math.Copysign(0, float64(rng.Float32()-0.5)))
		}
		return rng.Float32()*2 - 1
	}
	for _, n2 := range []int{6, 10, 14, 18, 30, 60, 120, 240, 480, 960} {
		n4 := n2 / 2
		trig := make([]float32, n2)
		spectrum := make([]float32, n2)
		fft := make([]kissCpx, n4)
		for i := range trig {
			trig[i], spectrum[i] = f(), f()
		}
		for i := range fft {
			fft[i] = kissCpx{f(), f()}
		}

		gotPre, wantPre := make([]complex64, n4), make([]complex64, n4)
		imdctPreRotateNoFMA(gotPre, spectrum, trig, n2, n4)
		imdctPreRotateNoFMAScalar(wantPre, spectrum, trig, n2, n4)
		for i := range wantPre {
			if math.Float32bits(real(gotPre[i])) != math.Float32bits(real(wantPre[i])) ||
				math.Float32bits(imag(gotPre[i])) != math.Float32bits(imag(wantPre[i])) {
				t.Fatalf("pre n2=%d: fftIn[%d] = %v, want %v", n2, i, gotPre[i], wantPre[i])
			}
		}

		gotPost, wantPost := make([]float32, n2), make([]float32, n2)
		imdctPostRotateF32FromKiss(gotPost, fft, trig, n2, n4)
		imdctPostRotateF32FromKissScalar(wantPost, fft, trig, n2, n4)
		for i := range wantPost {
			if math.Float32bits(gotPost[i]) != math.Float32bits(wantPost[i]) {
				t.Fatalf("post n2=%d: buf[%d] = %08x, want %08x", n2, i,
					math.Float32bits(gotPost[i]), math.Float32bits(wantPost[i]))
			}
		}
	}

	fftIn := make([]complex64, 480)
	spectrum, trig, buf := make([]float32, 960), make([]float32, 960), make([]float32, 960)
	fft := make([]kissCpx, 480)
	if allocs := testing.AllocsPerRun(100, func() {
		imdctPreRotateNoFMA(fftIn, spectrum, trig, 960, 480)
		imdctPostRotateF32FromKiss(buf, fft, trig, 960, 480)
	}); allocs != 0 {
		t.Fatalf("steady-state allocations = %g, want 0", allocs)
	}
}
