package celt

import (
	"math"
	"sync"
)

// NOTE ON APPARENT CODE DUPLICATION:
// The butterfly functions (kfBfly2, kfBfly3, kfBfly4, kfBfly5) and complex
// arithmetic helpers (cAdd, cSub, cMul) may appear similar to linters, but
// they implement mathematically distinct operations:
// - Each radix butterfly (2,3,4,5) has unique twiddle factor patterns
// - cAdd/cSub perform different arithmetic (+ vs -) on complex components
// - These functions are performance-critical FFT hot paths
// - Abstracting them would hurt performance and obscure the FFT algorithm
// This structure mirrors libopus kiss_fft.c for bit-exact compatibility.

// kissCpx mirrors kiss_fft_cpx for float builds.
type kissCpx struct {
	r float32
	i float32
}

// kissFFTState holds FFT tables and factors for a specific size.
type kissFFTState struct {
	nfft    int
	factors []int
	bitrev  []int
	w       []kissCpx
	fstride []int // Pre-computed fstride array for fftImpl (avoids per-call allocation)
}

var (
	kissFFTCache   = map[int]*kissFFTState{}
	kissFFTCacheMu sync.Mutex
)

func getKissFFTState(nfft int) *kissFFTState {
	kissFFTCacheMu.Lock()
	defer kissFFTCacheMu.Unlock()
	if st, ok := kissFFTCache[nfft]; ok {
		return st
	}
	st := newKissFFTState(nfft)
	kissFFTCache[nfft] = st
	return st
}

func newKissFFTState(nfft int) *kissFFTState {
	factors, ok := kfFactor(nfft)
	if !ok {
		return &kissFFTState{nfft: nfft}
	}
	bitrev := make([]int, nfft)
	computeBitrevTableRecursive(0, bitrev, 0, 1, 1, factors)
	w := computeTwiddles(nfft)

	// Pre-compute fstride array for fftImpl (eliminates per-call allocation)
	maxFactors := len(factors) / 2
	fstride := make([]int, maxFactors+1)
	fstride[0] = 1
	for i := 0; i < maxFactors; i++ {
		p := factors[2*i]
		fstride[i+1] = fstride[i] * p
	}

	return &kissFFTState{nfft: nfft, factors: factors, bitrev: bitrev, w: w, fstride: fstride}
}

// kfFactor computes the radix factors for kiss FFT.
func kfFactor(n int) ([]int, bool) {
	p := 4
	stages := 0
	nbak := n
	// allocate max factors (2*stages). For n<=480, 16 is enough.
	facbuf := make([]int, 32)
	for n > 1 {
		for n%p != 0 {
			switch p {
			case 4:
				p = 2
			case 2:
				p = 3
			default:
				p += 2
			}
			if p > 32000 || p*p > n {
				p = n
			}
		}
		n /= p
		if p > 5 { // unsupported radix
			return nil, false
		}
		facbuf[2*stages] = p
		if p == 2 && stages > 1 {
			facbuf[2*stages] = 4
			facbuf[2] = 2
		}
		stages++
	}
	// reverse order
	for i := 0; i < stages/2; i++ {
		tmp := facbuf[2*i]
		facbuf[2*i] = facbuf[2*(stages-i-1)]
		facbuf[2*(stages-i-1)] = tmp
	}
	// fill m values
	n = nbak
	for i := 0; i < stages; i++ {
		n /= facbuf[2*i]
		facbuf[2*i+1] = n
	}
	return facbuf[:2*stages], true
}

func computeTwiddles(nfft int) []kissCpx {
	w := make([]kissCpx, nfft)
	const pi = 3.14159265358979323846264338327
	for i := 0; i < nfft; i++ {
		phase := (-2.0 * pi / float64(nfft)) * float64(i)
		w[i].r = float32(math.Cos(phase))
		w[i].i = float32(math.Sin(phase))
	}
	return w
}

// computeBitrevTableRecursive fills the bitrev table using kiss FFT factor recursion.
// This mirrors the C version from kiss_fft.c:compute_bitrev_table.
//
// fout: starting output index (value to write)
// bitrev: the output array
// fIdx: starting write index in bitrev
// fstride: stride for this level
// inStride: always 1 for our use
// factors: [p, m, ...] pairs
//
// The function fills bitrev entries with stride fstride*inStride.
// When m==1 (leaf), it writes p consecutive values starting at fIdx.
// When m>1, it recurses p times with increased fstride.
func computeBitrevTableRecursive(fout int, bitrev []int, fIdx int, fstride int, inStride int, factors []int) {
	p := factors[0]
	m := factors[1]
	factors = factors[2:]
	step := fstride * inStride
	if m == 1 {
		// Leaf level: write p values with stride step
		for j := 0; j < p; j++ {
			if fIdx >= 0 && fIdx < len(bitrev) {
				bitrev[fIdx] = fout + j
			}
			fIdx += step
		}
		return
	}
	// Recursive level: call p times, advancing fIdx by step after each
	for j := 0; j < p; j++ {
		computeBitrevTableRecursive(fout, bitrev, fIdx, fstride*p, inStride, factors)
		fIdx += step
		fout += m
	}
}

func cAdd(a, b kissCpx) kissCpx {
	return kissCpx{r: a.r + b.r, i: a.i + b.i}
}

func cSub(a, b kissCpx) kissCpx {
	return kissCpx{r: a.r - b.r, i: a.i - b.i}
}

func cMul(a, b kissCpx) kissCpx {
	return kissCpx{r: a.r*b.r - a.i*b.i, i: a.r*b.i + a.i*b.r}
}

func cMulByScalar(a kissCpx, s float32) kissCpx {
	return kissCpx{r: a.r * s, i: a.i * s}
}

func kfBfly2M1Available() bool { return true }

func kfBfly4M1Available() bool { return true }

func kfBfly4MxAvailable() bool { return false }

func kfBfly3M1Available() bool { return true }

func kfBfly5M1Available() bool { return true }

// kfBfly2M1 handles the radix-2 m==1 hot path with index arithmetic (no reslicing).
func kfBfly2M1(fout []kissCpx, n int) {
	if n <= 0 {
		return
	}
	total := n << 1
	_ = fout[total-1] // BCE hint for i and i+1 accesses.
	for i := 0; i < total; i += 2 {
		ar := fout[i].r
		ai := fout[i].i
		br := fout[i+1].r
		bi := fout[i+1].i
		fout[i].r = ar + br
		fout[i].i = ai + bi
		fout[i+1].r = ar - br
		fout[i+1].i = ai - bi
	}
}

// kfBfly4M1 handles the radix-4 m==1 hot path.
func kfBfly4M1(fout []kissCpx, n int) {
	if n <= 0 {
		return
	}
	total := n << 2
	_ = fout[total-1] // BCE hint for base+0..3 accesses.
	for i := 0; i < total; i += 4 {
		a0r, a0i := fout[i].r, fout[i].i
		a1r, a1i := fout[i+1].r, fout[i+1].i
		a2r, a2i := fout[i+2].r, fout[i+2].i
		a3r, a3i := fout[i+3].r, fout[i+3].i

		s0r := a0r - a2r
		s0i := a0i - a2i
		f0r := a0r + a2r
		f0i := a0i + a2i

		s1r := a1r + a3r
		s1i := a1i + a3i
		f2r := f0r - s1r
		f2i := f0i - s1i
		f0r += s1r
		f0i += s1i

		s1r = a1r - a3r
		s1i = a1i - a3i
		f1r := s0r + s1i
		f1i := s0i - s1r
		f3r := s0r - s1i
		f3i := s0i + s1r

		fout[i].r, fout[i].i = f0r, f0i
		fout[i+1].r, fout[i+1].i = f1r, f1i
		fout[i+2].r, fout[i+2].i = f2r, f2i
		fout[i+3].r, fout[i+3].i = f3r, f3i
	}
}

// kfBfly3M1 handles the radix-3 m==1 path.
func kfBfly3M1(fout []kissCpx, tw []kissCpx, fstride, n, mm int) {
	if n <= 0 || mm <= 0 {
		return
	}
	last := (n-1)*mm + 2
	if last >= len(fout) || fstride >= len(tw) {
		return
	}
	epi3i := tw[fstride].i
	half := float32(0.5)
	_ = fout[last] // BCE hint for base+0..2 accesses.
	for i := 0; i < n; i++ {
		base := i * mm
		a0r, a0i := fout[base].r, fout[base].i
		a1r, a1i := fout[base+1].r, fout[base+1].i
		a2r, a2i := fout[base+2].r, fout[base+2].i

		s3r := a1r + a2r
		s3i := a1i + a2i
		s0r := a1r - a2r
		s0i := a1i - a2i

		f1r := a0r - half*s3r
		f1i := a0i - half*s3i
		f0r := a0r + s3r
		f0i := a0i + s3i

		s0r *= epi3i
		s0i *= epi3i

		f2r := f1r + s0i
		f2i := f1i - s0r
		f1r -= s0i
		f1i += s0r

		fout[base].r, fout[base].i = f0r, f0i
		fout[base+1].r, fout[base+1].i = f1r, f1i
		fout[base+2].r, fout[base+2].i = f2r, f2i
	}
}

// kfBfly5M1 handles the radix-5 m==1 path.
func kfBfly5M1(fout []kissCpx, tw []kissCpx, fstride, n, mm int) {
	if n <= 0 || mm <= 0 {
		return
	}
	last := (n-1)*mm + 4
	if last >= len(fout) || 2*fstride >= len(tw) {
		return
	}
	ya := tw[fstride]
	yb := tw[2*fstride]
	yar, yai := ya.r, ya.i
	ybr, ybi := yb.r, yb.i
	_ = fout[last] // BCE hint for base+0..4 accesses.
	for i := 0; i < n; i++ {
		base := i * mm
		a0r, a0i := fout[base].r, fout[base].i
		a1r, a1i := fout[base+1].r, fout[base+1].i
		a2r, a2i := fout[base+2].r, fout[base+2].i
		a3r, a3i := fout[base+3].r, fout[base+3].i
		a4r, a4i := fout[base+4].r, fout[base+4].i

		s7r, s7i := a1r+a4r, a1i+a4i
		s10r, s10i := a1r-a4r, a1i-a4i
		s8r, s8i := a2r+a3r, a2i+a3i
		s9r, s9i := a2r-a3r, a2i-a3i

		f0r := a0r + s7r + s8r
		f0i := a0i + s7i + s8i

		s5r := a0r + (s7r*yar + s8r*ybr)
		s5i := a0i + (s7i*yar + s8i*ybr)
		s6r := s10i*yai + s9i*ybi
		s6i := -(s10r*yai + s9r*ybi)

		f1r, f1i := s5r-s6r, s5i-s6i
		f4r, f4i := s5r+s6r, s5i+s6i

		s11r := a0r + (s7r*ybr + s8r*yar)
		s11i := a0i + (s7i*ybr + s8i*yar)
		s12r := s9i*yai - s10i*ybi
		s12i := s10r*ybi - s9r*yai

		f2r, f2i := s11r+s12r, s11i+s12i
		f3r, f3i := s11r-s12r, s11i-s12i

		fout[base].r, fout[base].i = f0r, f0i
		fout[base+1].r, fout[base+1].i = f1r, f1i
		fout[base+2].r, fout[base+2].i = f2r, f2i
		fout[base+3].r, fout[base+3].i = f3r, f3i
		fout[base+4].r, fout[base+4].i = f4r, f4i
	}
}

func kfBfly2(fout []kissCpx, m, N int) {
	if m == 1 {
		kfBfly2M1(fout, N)
		return
	}
	// m==4 degenerate radix-2 after radix-4
	tw := float32(0.7071067812)
	for i := 0; i < N; i++ {
		fout2 := fout[4:]
		t := fout2[0]
		fout2[0].r = fout[0].r - t.r
		fout2[0].i = fout[0].i - t.i
		fout[0].r += t.r
		fout[0].i += t.i

		t.r = (fout2[1].r + fout2[1].i) * tw
		t.i = (fout2[1].i - fout2[1].r) * tw
		fout2[1].r = fout[1].r - t.r
		fout2[1].i = fout[1].i - t.i
		fout[1].r += t.r
		fout[1].i += t.i

		t.r = fout2[2].i
		t.i = -fout2[2].r
		fout2[2].r = fout[2].r - t.r
		fout2[2].i = fout[2].i - t.i
		fout[2].r += t.r
		fout[2].i += t.i

		t.r = (fout2[3].i - fout2[3].r) * tw
		t.i = -(fout2[3].i + fout2[3].r) * tw
		fout2[3].r = fout[3].r - t.r
		fout2[3].i = fout[3].i - t.i
		fout[3].r += t.r
		fout[3].i += t.i

		fout = fout[8:]
	}
}

func kfBfly4(fout []kissCpx, fstride int, st *kissFFTState, m, N, mm int) {
	if m == 1 {
		kfBfly4M1(fout, N)
		return
	}
	m2 := 2 * m
	m3 := 3 * m
	if N <= 0 || mm <= 0 {
		return
	}
	_ = fout[N*mm-1] // BCE for idx+{0,m,m2,m3}
	w := st.w
	for i := 0; i < N; i++ {
		base := i * mm
		tw1, tw2, tw3 := 0, 0, 0
		for j := 0; j < m; j++ {
			idx := base + j

			a0r, a0i := fout[idx].r, fout[idx].i
			b1 := fout[idx+m]
			b2 := fout[idx+m2]
			b3 := fout[idx+m3]
			w1 := w[tw1]
			w2 := w[tw2]
			w3 := w[tw3]

			s0r := b1.r*w1.r - b1.i*w1.i
			s0i := b1.r*w1.i + b1.i*w1.r
			s1r := b2.r*w2.r - b2.i*w2.i
			s1i := b2.r*w2.i + b2.i*w2.r
			s2r := b3.r*w3.r - b3.i*w3.i
			s2i := b3.r*w3.i + b3.i*w3.r

			s5r := a0r - s1r
			s5i := a0i - s1i
			a0r += s1r
			a0i += s1i

			s3r := s0r + s2r
			s3i := s0i + s2i
			s4r := s0r - s2r
			s4i := s0i - s2i

			fout[idx+m2].r = a0r - s3r
			fout[idx+m2].i = a0i - s3i
			a0r += s3r
			a0i += s3i
			fout[idx].r = a0r
			fout[idx].i = a0i

			fout[idx+m].r = s5r + s4i
			fout[idx+m].i = s5i - s4r
			fout[idx+m3].r = s5r - s4i
			fout[idx+m3].i = s5i + s4r

			tw1 += fstride
			tw2 += fstride * 2
			tw3 += fstride * 3
		}
	}
}

func kfBfly3(fout []kissCpx, fstride int, st *kissFFTState, m, N, mm int) {
	m2 := 2 * m
	epi3 := st.w[fstride*m]
	if N <= 0 || mm <= 0 {
		return
	}
	_ = fout[N*mm-1] // BCE for idx+{0,m,m2}
	w := st.w
	const half = float32(0.5)
	epi3i := epi3.i
	for i := 0; i < N; i++ {
		base := i * mm
		tw1, tw2 := 0, 0
		for j := 0; j < m; j++ {
			idx := base + j

			a0r, a0i := fout[idx].r, fout[idx].i
			b1 := fout[idx+m]
			b2 := fout[idx+m2]
			w1 := w[tw1]
			w2 := w[tw2]

			s1r := b1.r*w1.r - b1.i*w1.i
			s1i := b1.r*w1.i + b1.i*w1.r
			s2r := b2.r*w2.r - b2.i*w2.i
			s2i := b2.r*w2.i + b2.i*w2.r

			s3r := s1r + s2r
			s3i := s1i + s2i
			s0r := (s1r - s2r) * epi3i
			s0i := (s1i - s2i) * epi3i

			f1r := a0r - half*s3r
			f1i := a0i - half*s3i
			fout[idx].r = a0r + s3r
			fout[idx].i = a0i + s3i
			fout[idx+m2].r = f1r + s0i
			fout[idx+m2].i = f1i - s0r
			fout[idx+m].r = f1r - s0i
			fout[idx+m].i = f1i + s0r

			tw1 += fstride
			tw2 += fstride * 2
		}
	}
}

func kfBfly5(fout []kissCpx, fstride int, st *kissFFTState, m, N, mm int) {
	if N <= 0 || mm <= 0 {
		return
	}
	ya := st.w[fstride*m]
	yb := st.w[fstride*2*m]
	yar, yai := ya.r, ya.i
	ybr, ybi := yb.r, yb.i
	_ = fout[N*mm-1] // BCE for idx+{0..4m}
	w := st.w
	for i := 0; i < N; i++ {
		base := i * mm
		idx0, idx1, idx2, idx3, idx4 := base, base+m, base+2*m, base+3*m, base+4*m
		tw1, tw2, tw3, tw4 := 0, 0, 0, 0
		for u := 0; u < m; u++ {
			a0 := fout[idx0]
			b1 := fout[idx1]
			b2 := fout[idx2]
			b3 := fout[idx3]
			b4 := fout[idx4]
			w1 := w[tw1]
			w2 := w[tw2]
			w3 := w[tw3]
			w4 := w[tw4]

			s1r := b1.r*w1.r - b1.i*w1.i
			s1i := b1.r*w1.i + b1.i*w1.r
			s2r := b2.r*w2.r - b2.i*w2.i
			s2i := b2.r*w2.i + b2.i*w2.r
			s3r := b3.r*w3.r - b3.i*w3.i
			s3i := b3.r*w3.i + b3.i*w3.r
			s4r := b4.r*w4.r - b4.i*w4.i
			s4i := b4.r*w4.i + b4.i*w4.r

			s7r, s7i := s1r+s4r, s1i+s4i
			s10r, s10i := s1r-s4r, s1i-s4i
			s8r, s8i := s2r+s3r, s2i+s3i
			s9r, s9i := s2r-s3r, s2i-s3i

			fout[idx0].r = a0.r + (s7r + s8r)
			fout[idx0].i = a0.i + (s7i + s8i)

			s5r := a0.r + (s7r*yar + s8r*ybr)
			s5i := a0.i + (s7i*yar + s8i*ybr)
			s6r := s10i*yai + s9i*ybi
			s6i := -(s10r*yai + s9r*ybi)
			fout[idx1].r, fout[idx1].i = s5r-s6r, s5i-s6i
			fout[idx4].r, fout[idx4].i = s5r+s6r, s5i+s6i

			s11r := a0.r + (s7r*ybr + s8r*yar)
			s11i := a0.i + (s7i*ybr + s8i*yar)
			s12r := s9i*yai - s10i*ybi
			s12i := s10r*ybi - s9r*yai
			fout[idx2].r, fout[idx2].i = s11r+s12r, s11i+s12i
			fout[idx3].r, fout[idx3].i = s11r-s12r, s11i-s12i

			idx0++
			idx1++
			idx2++
			idx3++
			idx4++
			tw1 += fstride
			tw2 += fstride * 2
			tw3 += fstride * 3
			tw4 += fstride * 4
		}
	}
}

func (st *kissFFTState) fftImpl(fout []kissCpx) {
	if st == nil || st.nfft == 0 {
		return
	}
	// Use pre-computed fstride array (avoids per-call allocation)
	fstride := st.fstride
	if len(fstride) == 0 {
		return
	}

	// Find L by walking factors until m == 1
	L := 0
	for {
		if 2*L+1 >= len(st.factors) {
			break
		}
		m := st.factors[2*L+1]
		L++
		if m == 1 {
			break
		}
	}
	if L == 0 {
		return
	}

	m := st.factors[2*L-1]
	for i := L - 1; i >= 0; i-- {
		m2 := 1
		if i != 0 {
			m2 = st.factors[2*i-1]
		}
		switch st.factors[2*i] {
		case 2:
			kfBfly2(fout, m, fstride[i])
		case 4:
			kfBfly4(fout, fstride[i], st, m, fstride[i], m2)
		case 3:
			kfBfly3(fout, fstride[i], st, m, fstride[i], m2)
		case 5:
			kfBfly5(fout, fstride[i], st, m, fstride[i], m2)
		}
		m = m2
	}
}

// kissFFT32 is a drop-in replacement for dft32 using the Kiss FFT algorithm.
// It takes complex64 input and returns complex64 output, matching the MDCT code interface.
// This uses the Kiss FFT butterfly functions which match libopus exactly.
//
// Note: The scaling (1/n) is NOT applied here - the caller (MDCT) handles scaling.
// This matches libopus behavior where opus_fft_impl doesn't scale.
func kissFFT32(x []complex64) []complex64 {
	n := len(x)
	if n == 0 {
		return nil
	}
	out := make([]complex64, n)
	tmp := make([]kissCpx, n)
	kissFFT32To(out, x, tmp)
	return out
}

// kissFFT32To performs the Kiss FFT into a caller-provided output buffer.
// scratch must be at least len(x) to avoid allocations.
func kissFFT32To(out []complex64, x []complex64, scratch []kissCpx) {
	n := len(x)
	if n == 0 || len(out) < n {
		return
	}

	st := getKissFFTState(n)
	if st == nil || len(st.bitrev) != n {
		// Fallback to direct DFT
		dft32FallbackTo(out, x)
		return
	}

	if len(scratch) < n {
		scratch = make([]kissCpx, n)
	}

	// Convert to kissCpx and apply bit-reversal
	for i := 0; i < n; i++ {
		v := x[i]
		idx := st.bitrev[i]
		scratch[idx].r = real(v)
		scratch[idx].i = imag(v)
	}

	// Apply butterfly stages
	st.fftImpl(scratch[:n])

	// Convert back to complex64
	for i := 0; i < n; i++ {
		out[i] = complex(scratch[i].r, scratch[i].i)
	}
}

// kissFFT32ToInterleaved performs the Kiss FFT and writes output as interleaved
// real/imag float32 pairs into outRI: [re0, im0, re1, im1, ...].
// outRI must have length at least 2*len(x).
func kissFFT32ToInterleaved(outRI []float32, x []complex64, scratch []kissCpx) {
	n := len(x)
	if n == 0 || len(outRI) < 2*n {
		return
	}

	st := getKissFFTState(n)
	if st == nil || len(st.bitrev) != n {
		// Fallback to direct DFT and interleave.
		tmp := make([]complex64, n)
		dft32FallbackTo(tmp, x)
		j := 0
		for i := 0; i < n; i++ {
			v := tmp[i]
			outRI[j] = real(v)
			outRI[j+1] = imag(v)
			j += 2
		}
		return
	}

	if len(scratch) < n {
		scratch = make([]kissCpx, n)
	}

	// Convert to kissCpx and apply bit-reversal.
	for i := 0; i < n; i++ {
		v := x[i]
		idx := st.bitrev[i]
		scratch[idx].r = real(v)
		scratch[idx].i = imag(v)
	}

	// Apply butterfly stages.
	st.fftImpl(scratch[:n])

	// Interleave output directly into float32 buffer.
	j := 0
	for i := 0; i < n; i++ {
		v := scratch[i]
		outRI[j] = v.r
		outRI[j+1] = v.i
		j += 2
	}
}

// dft32Fallback is a direct O(n^2) DFT implementation as fallback.
func dft32Fallback(x []complex64) []complex64 {
	n := len(x)
	if n <= 1 {
		return x
	}

	out := make([]complex64, n)
	dft32FallbackTo(out, x)
	return out
}

func dft32FallbackTo(out []complex64, x []complex64) {
	n := len(x)
	if n == 0 || len(out) < n {
		return
	}
	if n == 1 {
		out[0] = x[0]
		return
	}
	twoPi := float32(-2.0*math.Pi) / float32(n)
	for k := 0; k < n; k++ {
		angle := twoPi * float32(k)
		wStep := complex(float32(math.Cos(float64(angle))), float32(math.Sin(float64(angle))))
		w := complex(float32(1.0), float32(0.0))
		var sum complex64
		for t := 0; t < n; t++ {
			sum += x[t] * w
			w *= wStep
		}
		out[k] = sum
	}
}
