//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// kfBfly4MxRef is the reference Go implementation for testing the assembly version.
func kfBfly4MxRef(fout []kissCpx, tw []kissCpx, m, n, fstride, mm int) {
	foutBeg := fout
	for i := 0; i < n; i++ {
		f := foutBeg[i*mm:]
		tw1 := 0
		tw2 := 0
		tw3 := 0
		for j := 0; j < m; j++ {
			scratch0 := cMul(f[m], tw[tw1])
			scratch1 := cMul(f[2*m], tw[tw2])
			scratch2 := cMul(f[3*m], tw[tw3])
			scratch5 := cSub(f[0], scratch1)
			f[0] = cAdd(f[0], scratch1)
			scratch3 := cAdd(scratch0, scratch2)
			scratch4 := cSub(scratch0, scratch2)
			f[2*m] = cSub(f[0], scratch3)
			f[0] = cAdd(f[0], scratch3)
			f[m].r = scratch5.r + scratch4.i
			f[m].i = scratch5.i - scratch4.r
			f[3*m].r = scratch5.r - scratch4.i
			f[3*m].i = scratch5.i + scratch4.r
			tw1 += fstride
			tw2 += fstride * 2
			tw3 += fstride * 3
			f = f[1:]
		}
	}
}

func TestKfBfly4MxM1(t *testing.T) {
	// Minimal test: m=1, n=1 (single butterfly)
	m := 1
	n := 1
	fstride := 1
	mm := 4

	// Create deterministic input: 4 complex numbers for one butterfly
	fout := make([]kissCpx, 4)
	fout[0] = kissCpx{r: 1.0, i: 2.0}
	fout[1] = kissCpx{r: 3.0, i: 4.0}
	fout[2] = kissCpx{r: 5.0, i: 6.0}
	fout[3] = kissCpx{r: 7.0, i: 8.0}

	tw := make([]kissCpx, 10)
	tw[0] = kissCpx{r: 1.0, i: 0.0}  // tw[0] = 1+0i (identity for multiplication)
	tw[1] = kissCpx{r: 1.0, i: 0.0}
	tw[2] = kissCpx{r: 1.0, i: 0.0}
	tw[3] = kissCpx{r: 1.0, i: 0.0}

	inRef := make([]kissCpx, len(fout))
	inAsm := make([]kissCpx, len(fout))
	copy(inRef, fout)
	copy(inAsm, fout)

	t.Logf("Before: %v", inRef)

	kfBfly4MxRef(inRef, tw, m, n, fstride, mm)
	kfBfly4Mx(inAsm, tw, m, n, fstride, mm)

	t.Logf("After Ref: %v", inRef)
	t.Logf("After Asm: %v", inAsm)

	for j := range inRef {
		dr := math.Abs(float64(inRef[j].r - inAsm[j].r))
		di := math.Abs(float64(inRef[j].i - inAsm[j].i))
		if dr > 1e-5 || di > 1e-5 {
			t.Errorf("[%d] ref=(%f,%f) asm=(%f,%f) diff=(%f,%f)", j, inRef[j].r, inRef[j].i, inAsm[j].r, inAsm[j].i, dr, di)
		}
	}
}

func TestKfBfly4MxM2(t *testing.T) {
	// Test with m=2, n=1 (two inner loop iterations)
	m := 2
	n := 1
	fstride := 1
	mm := 8

	// Need 8 elements: indices 0,1 are f[0:2], 2,3 are f[m:m+2], 4,5 are f[2m:2m+2], 6,7 are f[3m:3m+2]
	fout := make([]kissCpx, 8)
	for i := range fout {
		fout[i] = kissCpx{r: float32(i), i: float32(i) + 0.5}
	}

	tw := make([]kissCpx, 10)
	for i := range tw {
		tw[i] = kissCpx{r: 1.0, i: 0.0} // All twiddles are 1
	}

	inRef := make([]kissCpx, len(fout))
	inAsm := make([]kissCpx, len(fout))
	copy(inRef, fout)
	copy(inAsm, fout)

	t.Logf("Before: %v", inRef)

	kfBfly4MxRef(inRef, tw, m, n, fstride, mm)
	kfBfly4Mx(inAsm, tw, m, n, fstride, mm)

	t.Logf("After Ref: %v", inRef)
	t.Logf("After Asm: %v", inAsm)

	for j := range inRef {
		dr := math.Abs(float64(inRef[j].r - inAsm[j].r))
		di := math.Abs(float64(inRef[j].i - inAsm[j].i))
		if dr > 1e-5 || di > 1e-5 {
			t.Errorf("[%d] ref=(%f,%f) asm=(%f,%f) diff=(%f,%f)", j, inRef[j].r, inRef[j].i, inAsm[j].r, inAsm[j].i, dr, di)
		}
	}
}

func TestKfBfly4MxSimple(t *testing.T) {
	// Simple test with n=1 to focus on inner loop
	m := 4
	n := 1
	fstride := 15
	mm := 16

	// Create deterministic input
	fout := make([]kissCpx, 16)
	tw := make([]kissCpx, 200) // tw3 goes up to fstride*3*(m-1) = 15*3*3 = 135
	for i := range fout {
		fout[i] = kissCpx{r: float32(i), i: float32(i) + 0.5}
	}
	for i := range tw {
		tw[i] = kissCpx{r: float32(i%5) * 0.1, i: float32(i%7) * 0.1}
	}

	inRef := make([]kissCpx, len(fout))
	inAsm := make([]kissCpx, len(fout))
	copy(inRef, fout)
	copy(inAsm, fout)

	t.Logf("Before: fout[0:4]=%v fout[4:8]=%v fout[8:12]=%v fout[12:16]=%v", inRef[0:4], inRef[4:8], inRef[8:12], inRef[12:16])

	kfBfly4MxRef(inRef, tw, m, n, fstride, mm)
	kfBfly4Mx(inAsm, tw, m, n, fstride, mm)

	t.Logf("After Ref: fout[0:4]=%v fout[4:8]=%v fout[8:12]=%v fout[12:16]=%v", inRef[0:4], inRef[4:8], inRef[8:12], inRef[12:16])
	t.Logf("After Asm: fout[0:4]=%v fout[4:8]=%v fout[8:12]=%v fout[12:16]=%v", inAsm[0:4], inAsm[4:8], inAsm[8:12], inAsm[12:16])

	for j := range inRef {
		dr := math.Abs(float64(inRef[j].r - inAsm[j].r))
		di := math.Abs(float64(inRef[j].i - inAsm[j].i))
		if dr > 1e-5 || di > 1e-5 {
			t.Errorf("[%d] ref=(%f,%f) asm=(%f,%f) diff=(%f,%f)", j, inRef[j].r, inRef[j].i, inAsm[j].r, inAsm[j].i, dr, di)
		}
	}
}

func TestKfBfly4MxAgainstRef(t *testing.T) {
	const nfft = 240
	st := getKissFFTState(nfft)
	if st == nil {
		t.Fatal("nil fft state")
	}
	// Build fstride array like fftImpl
	maxFactors := len(st.factors) / 2
	fstride := make([]int, maxFactors+1)
	fstride[0] = 1
	L := 0
	m := 0
	for {
		p := st.factors[2*L]
		m = st.factors[2*L+1]
		fstride[L+1] = fstride[L] * p
		L++
		if m == 1 {
			break
		}
	}
	m = st.factors[2*L-1]

	rng := rand.New(rand.NewSource(123))
	base := make([]kissCpx, nfft)
	for i := range base {
		base[i] = kissCpx{r: float32(rng.Float64()*2 - 1), i: float32(rng.Float64()*2 - 1)}
	}

	for i := L - 1; i >= 0; i-- {
		m2 := 1
		if i != 0 {
			m2 = st.factors[2*i-1]
		}
		if st.factors[2*i] != 4 {
			m = m2
			continue
		}
		if m == 1 {
			m = m2
			continue
		}

		inRef := make([]kissCpx, len(base))
		inAsm := make([]kissCpx, len(base))
		copy(inRef, base)
		copy(inAsm, base)

		kfBfly4MxRef(inRef, st.w, m, fstride[i], fstride[i], m2)
		kfBfly4Mx(inAsm, st.w, m, fstride[i], fstride[i], m2)

		maxDiff := float64(0)
		firstMismatchIdx := -1
		for j := range inRef {
			dr := math.Abs(float64(inRef[j].r - inAsm[j].r))
			di := math.Abs(float64(inRef[j].i - inAsm[j].i))
			if dr > maxDiff {
				maxDiff = dr
			}
			if di > maxDiff {
				maxDiff = di
			}
			if (dr > 1e-5 || di > 1e-5) && firstMismatchIdx < 0 {
				firstMismatchIdx = j
			}
		}
		if maxDiff > 1e-5 {
			t.Logf("First mismatch at index %d", firstMismatchIdx)
			t.Logf("  Ref: r=%f i=%f", inRef[firstMismatchIdx].r, inRef[firstMismatchIdx].i)
			t.Logf("  Asm: r=%f i=%f", inAsm[firstMismatchIdx].r, inAsm[firstMismatchIdx].i)
			// Print a few surrounding indices
			for idx := max(0, firstMismatchIdx-2); idx < min(len(inRef), firstMismatchIdx+5); idx++ {
				dr := math.Abs(float64(inRef[idx].r - inAsm[idx].r))
				di := math.Abs(float64(inRef[idx].i - inAsm[idx].i))
				t.Logf("  [%d] ref=(%f,%f) asm=(%f,%f) diff=(%f,%f)", idx, inRef[idx].r, inRef[idx].i, inAsm[idx].r, inAsm[idx].i, dr, di)
			}
			t.Fatalf("kfBfly4Mx mismatch: stage i=%d m=%d n=%d fstride=%d mm=%d diff=%.6f", i, m, fstride[i], fstride[i], m2, maxDiff)
		}

		m = m2
	}
}
