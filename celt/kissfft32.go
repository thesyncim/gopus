package celt

import (
	"math"
	"sync"
)

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

func kfBfly2(fout []kissCpx, m, N int) {
	if m == 1 {
		if kfBfly2M1Available() {
			kfBfly2M1(fout, N)
			return
		}
		for i := 0; i < N; i++ {
			fout2 := fout[1]
			fout[1].r = fout[0].r - fout2.r
			fout[1].i = fout[0].i - fout2.i
			fout[0].r += fout2.r
			fout[0].i += fout2.i
			fout = fout[2:]
		}
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
		if kfBfly4M1Available() {
			kfBfly4M1(fout, N)
			return
		}
		for i := 0; i < N; i++ {
			scratch0 := cSub(fout[0], fout[2])
			fout[0] = cAdd(fout[0], fout[2])
			scratch1 := cAdd(fout[1], fout[3])
			fout[2] = cSub(fout[0], scratch1)
			fout[0] = cAdd(fout[0], scratch1)
			scratch1 = cSub(fout[1], fout[3])
			fout[1].r = scratch0.r + scratch1.i
			fout[1].i = scratch0.i - scratch1.r
			fout[3].r = scratch0.r - scratch1.i
			fout[3].i = scratch0.i + scratch1.r
			fout = fout[4:]
		}
		return
	}
	if kfBfly4MxAvailable() {
		kfBfly4Mx(fout, st.w, m, N, fstride, mm)
		return
	}
	m2 := 2 * m
	m3 := 3 * m
	foutBeg := fout
	for i := 0; i < N; i++ {
		fout = foutBeg[i*mm:]
		tw1 := 0
		tw2 := 0
		tw3 := 0
		for j := 0; j < m; j++ {
			scratch0 := cMul(fout[m], st.w[tw1])
			scratch1 := cMul(fout[m2], st.w[tw2])
			scratch2 := cMul(fout[m3], st.w[tw3])
			scratch5 := cSub(fout[0], scratch1)
			fout[0] = cAdd(fout[0], scratch1)
			scratch3 := cAdd(scratch0, scratch2)
			scratch4 := cSub(scratch0, scratch2)
			fout[m2] = cSub(fout[0], scratch3)
			tw1 += fstride
			tw2 += fstride * 2
			tw3 += fstride * 3
			fout[0] = cAdd(fout[0], scratch3)
			fout[m].r = scratch5.r + scratch4.i
			fout[m].i = scratch5.i - scratch4.r
			fout[m3].r = scratch5.r - scratch4.i
			fout[m3].i = scratch5.i + scratch4.r
			fout = fout[1:]
		}
	}
}

func kfBfly3(fout []kissCpx, fstride int, st *kissFFTState, m, N, mm int) {
	if m == 1 {
		if kfBfly3M1Available() {
			kfBfly3M1(fout, st.w, fstride, N, mm)
			return
		}
	}
	m2 := 2 * m
	foutBeg := fout
	epi3 := st.w[fstride*m]
	for i := 0; i < N; i++ {
		fout = foutBeg[i*mm:]
		tw1 := 0
		tw2 := 0
		for k := 0; k < m; k++ {
			scratch1 := cMul(fout[m], st.w[tw1])
			scratch2 := cMul(fout[m2], st.w[tw2])
			scratch3 := cAdd(scratch1, scratch2)
			scratch0 := cSub(scratch1, scratch2)
			tw1 += fstride
			tw2 += fstride * 2

			// HALF_OF(x) = x * 0.5f in float builds
			fout[m].r = fout[0].r - float32(0.5)*scratch3.r
			fout[m].i = fout[0].i - float32(0.5)*scratch3.i
			scratch0 = cMulByScalar(scratch0, epi3.i)
			fout[0].r += scratch3.r
			fout[0].i += scratch3.i

			fout[m2].r = fout[m].r + scratch0.i
			fout[m2].i = fout[m].i - scratch0.r
			fout[m].r = fout[m].r - scratch0.i
			fout[m].i = fout[m].i + scratch0.r

			fout = fout[1:]
		}
	}
}

func kfBfly5(fout []kissCpx, fstride int, st *kissFFTState, m, N, mm int) {
	if m == 1 {
		if kfBfly5M1Available() {
			kfBfly5M1(fout, st.w, fstride, N, mm)
			return
		}
	}
	foutBeg := fout
	ya := st.w[fstride*m]
	yb := st.w[fstride*2*m]
	for i := 0; i < N; i++ {
		fout = foutBeg[i*mm:]
		fout0 := fout
		fout1 := fout[m:]
		fout2 := fout[2*m:]
		fout3 := fout[3*m:]
		fout4 := fout[4*m:]
		for u := 0; u < m; u++ {
			scratch0 := fout0[0]
			scratch1 := cMul(fout1[0], st.w[u*fstride])
			scratch2 := cMul(fout2[0], st.w[2*u*fstride])
			scratch3 := cMul(fout3[0], st.w[3*u*fstride])
			scratch4 := cMul(fout4[0], st.w[4*u*fstride])

			scratch7 := cAdd(scratch1, scratch4)
			scratch10 := cSub(scratch1, scratch4)
			scratch8 := cAdd(scratch2, scratch3)
			scratch9 := cSub(scratch2, scratch3)

			fout0[0].r = fout0[0].r + (scratch7.r + scratch8.r)
			fout0[0].i = fout0[0].i + (scratch7.i + scratch8.i)

			scratch5 := kissCpx{
				r: scratch0.r + (scratch7.r*ya.r + scratch8.r*yb.r),
				i: scratch0.i + (scratch7.i*ya.r + scratch8.i*yb.r),
			}
			scratch6 := kissCpx{
				r: scratch10.i*ya.i + scratch9.i*yb.i,
				i: -(scratch10.r*ya.i + scratch9.r*yb.i),
			}

			fout1[0] = cSub(scratch5, scratch6)
			fout4[0] = cAdd(scratch5, scratch6)

			scratch11 := kissCpx{
				r: scratch0.r + (scratch7.r*yb.r + scratch8.r*ya.r),
				i: scratch0.i + (scratch7.i*yb.r + scratch8.i*ya.r),
			}
			scratch12 := kissCpx{
				r: scratch9.i*ya.i - scratch10.i*yb.i,
				i: scratch10.r*yb.i - scratch9.r*ya.i,
			}

			fout2[0] = cAdd(scratch11, scratch12)
			fout3[0] = cSub(scratch11, scratch12)

			fout0 = fout0[1:]
			fout1 = fout1[1:]
			fout2 = fout2[1:]
			fout3 = fout3[1:]
			fout4 = fout4[1:]
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

// opusFFT performs forward FFT matching libopus opus_fft_c.
// fin: input array (length nfft)
// Returns: FFT output (length nfft)
//
// The FFT applies:
// 1. Scaling by 1/nfft
// 2. Bit-reversal permutation
// 3. Decimation-in-frequency butterfly stages
//
// Note: This is the standalone FFT function. For MDCT use opusFFTImplBitrev
// which applies bit-reversal but no scaling (scaling is done in MDCT pre-rotation).
func opusFFT(fin []kissCpx) []kissCpx {
	nfft := len(fin)
	if nfft == 0 {
		return nil
	}

	st := getKissFFTState(nfft)
	if st == nil || len(st.bitrev) != nfft {
		// Fallback to direct DFT if factors not supported
		return opusFFTDirect(fin)
	}

	// Scale factor: 1/nfft (libopus float path)
	scale := float32(1.0 / float64(nfft))

	// Bit-reverse and scale the input
	fout := make([]kissCpx, nfft)
	for i := 0; i < nfft; i++ {
		x := fin[i]
		fout[st.bitrev[i]].r = x.r * scale
		fout[st.bitrev[i]].i = x.i * scale
	}

	// Apply butterfly stages
	st.fftImpl(fout)

	return fout
}

// opusFFTImplBitrev performs FFT with bit-reversal but no scaling.
// This matches the pattern used in libopus clt_mdct_forward where:
// - Scaling is applied during pre-rotation
// - Bit-reversal is done when storing pre-rotation results
// - opus_fft_impl is called on the already bit-reversed data
//
// fout: pre-bit-reversed data (modified in place)
func opusFFTImplBitrev(fout []kissCpx) {
	nfft := len(fout)
	if nfft == 0 {
		return
	}

	st := getKissFFTState(nfft)
	if st == nil || len(st.factors) == 0 {
		// Fallback: compute DFT directly
		result := opusFFTDirectNoScale(fout)
		copy(fout, result)
		return
	}

	// Apply butterfly stages (data is already bit-reversed)
	st.fftImpl(fout)
}

// opusFFTDirectNoScale is a fallback O(n^2) DFT without scaling.
func opusFFTDirectNoScale(fin []kissCpx) []kissCpx {
	n := len(fin)
	if n == 0 {
		return nil
	}

	out := make([]kissCpx, n)
	twoPi := float32(-2.0 * math.Pi / float64(n))

	for k := 0; k < n; k++ {
		angle := twoPi * float32(k)
		wr := float32(math.Cos(float64(angle)))
		wi := float32(math.Sin(float64(angle)))
		var sumR, sumI float32
		wRe := float32(1.0)
		wIm := float32(0.0)
		for t := 0; t < n; t++ {
			sumR += fin[t].r*wRe - fin[t].i*wIm
			sumI += fin[t].r*wIm + fin[t].i*wRe
			// w *= wStep
			newRe := wRe*wr - wIm*wi
			newIm := wRe*wi + wIm*wr
			wRe, wIm = newRe, newIm
		}
		out[k].r = sumR
		out[k].i = sumI
	}
	return out
}

// opusIFFT performs inverse FFT matching libopus opus_ifft_c.
// fin: input array (length nfft)
// Returns: IFFT output (length nfft)
//
// The IFFT applies:
// 1. Bit-reversal permutation
// 2. Sign flip on imaginary part
// 3. Decimation-in-frequency butterfly stages
// 4. Sign flip on imaginary part
func opusIFFT(fin []kissCpx) []kissCpx {
	nfft := len(fin)
	if nfft == 0 {
		return nil
	}

	st := getKissFFTState(nfft)
	if st == nil || len(st.bitrev) != nfft {
		// Fallback to direct IDFT if factors not supported
		return opusIFFTDirect(fin)
	}

	// Bit-reverse the input
	fout := make([]kissCpx, nfft)
	for i := 0; i < nfft; i++ {
		fout[st.bitrev[i]] = fin[i]
	}

	// Conjugate (negate imaginary)
	for i := 0; i < nfft; i++ {
		fout[i].i = -fout[i].i
	}

	// Apply butterfly stages
	st.fftImpl(fout)

	// Conjugate again (negate imaginary)
	for i := 0; i < nfft; i++ {
		fout[i].i = -fout[i].i
	}

	return fout
}

// opusFFTDirect is a fallback O(n^2) DFT for unsupported sizes.
func opusFFTDirect(fin []kissCpx) []kissCpx {
	n := len(fin)
	if n == 0 {
		return nil
	}

	out := make([]kissCpx, n)
	scale := float32(1.0 / float64(n))
	twoPi := float32(-2.0 * math.Pi / float64(n))

	for k := 0; k < n; k++ {
		angle := twoPi * float32(k)
		wr := float32(math.Cos(float64(angle)))
		wi := float32(math.Sin(float64(angle)))
		var sumR, sumI float32
		wRe := float32(1.0)
		wIm := float32(0.0)
		for t := 0; t < n; t++ {
			sumR += fin[t].r*wRe - fin[t].i*wIm
			sumI += fin[t].r*wIm + fin[t].i*wRe
			// w *= wStep
			newRe := wRe*wr - wIm*wi
			newIm := wRe*wi + wIm*wr
			wRe, wIm = newRe, newIm
		}
		out[k].r = sumR * scale
		out[k].i = sumI * scale
	}
	return out
}

// opusIFFTDirect is a fallback O(n^2) IDFT for unsupported sizes.
func opusIFFTDirect(fin []kissCpx) []kissCpx {
	n := len(fin)
	if n == 0 {
		return nil
	}

	out := make([]kissCpx, n)
	twoPi := float32(2.0 * math.Pi / float64(n)) // positive for inverse

	for k := 0; k < n; k++ {
		angle := twoPi * float32(k)
		wr := float32(math.Cos(float64(angle)))
		wi := float32(math.Sin(float64(angle)))
		var sumR, sumI float32
		wRe := float32(1.0)
		wIm := float32(0.0)
		for t := 0; t < n; t++ {
			sumR += fin[t].r*wRe - fin[t].i*wIm
			sumI += fin[t].r*wIm + fin[t].i*wRe
			// w *= wStep
			newRe := wRe*wr - wIm*wi
			newIm := wRe*wi + wIm*wr
			wRe, wIm = newRe, newIm
		}
		out[k].r = sumR
		out[k].i = sumI
	}
	return out
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
