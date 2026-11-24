package celt

import (
	"math"

	"github.com/thesyncim/gopus/internal/opusmath"
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
	shift   int
	factors []int
	bitrev  []int
	w       []kissCpx
	fstride []int // Pre-computed fstride array for fftImpl (avoids per-call allocation)
	// stageTw holds each factor stage's twiddles packed for the Fast
	// butterflies; see kissStageTwiddles.
	stageTw []kissStageTwiddles
}

var (
	// Common CELT FFT sizes are prebuilt once and reused lock-free.
	kissFFTState60  = newKissFFTState(60)
	kissFFTState120 = newKissFFTState(120)
	kissFFTState240 = newKissFFTState(240)
	kissFFTState480 = newKissFFTState(480)
)

// kissScaleMul marks multiply sites that stay rounded before their consumers
// in the selected libopus target.
func kissScaleMul(a, b float32) float32 {
	return round32(a * b)
}

func kissAdd(a, b float32) float32 {
	return a + b
}

func kissSub(a, b float32) float32 {
	return a - b
}

func getKissFFTState(nfft int) *kissFFTState {
	switch nfft {
	case 960:
		return getKissFFTState960()
	case 320:
		return getKissFFTState320()
	case 60:
		return kissFFTState60
	case 120:
		return kissFFTState120
	case 240:
		return kissFFTState240
	case 480:
		return kissFFTState480
	default:
		// Keep uncommon/test-only sizes working without global mutable caches.
		return newKissFFTState(nfft)
	}
}

func newKissFFTState(nfft int) *kissFFTState {
	if st := newStaticKissFFTState(nfft); st != nil {
		return st
	}
	return newDynamicKissFFTState(nfft, nil)
}

// newDynamicKissFFTState follows opus_fft_alloc_twiddles: smaller transforms
// share their mode's base table, with a power-of-two index shift.
func newDynamicKissFFTState(nfft int, base *kissFFTState) *kissFFTState {
	factors, ok := kfFactor(nfft)
	if !ok {
		return &kissFFTState{nfft: nfft}
	}
	bitrev := make([]int, nfft)
	computeBitrevTableRecursive(0, bitrev, 0, 1, 1, factors)
	var w []kissCpx
	shift := -1
	if base == nil {
		w = computeTwiddles(nfft)
	} else {
		shift = 0
		for shift < 32 && nfft<<shift != base.nfft {
			shift++
		}
		if shift == 32 {
			return nil
		}
		w = base.w
	}

	// Pre-compute fstride array for fftImpl (eliminates per-call allocation)
	maxFactors := len(factors) / 2
	fstride := make([]int, maxFactors+1)
	fstride[0] = 1
	for i := range maxFactors {
		p := factors[2*i]
		fstride[i+1] = fstride[i] * p
	}

	return &kissFFTState{nfft: nfft, shift: shift, factors: factors, bitrev: bitrev, w: w, fstride: fstride,
		stageTw: newKissStageTwiddles(factors, fstride, shift, w)}
}

func newStaticKissFFTState(nfft int) *kissFFTState {
	var factors []int
	var bitrev []int
	var twiddles []kissCpx
	shift := 0
	switch nfft {
	case 320:
		bitrev = staticKissFFT320Bitrev()
		twiddles = staticKissFFT320Twiddles()
		if bitrev == nil || twiddles == nil {
			return nil
		}
		factors = []int{5, 64, 4, 16, 4, 4, 4, 1}
		shift = -1
	case 480:
		factors = []int{5, 96, 3, 32, 4, 8, 2, 4, 4, 1}
		shift = 0
	case 240:
		factors = []int{5, 48, 3, 16, 4, 4, 4, 1}
		shift = 1
	case 120:
		factors = []int{5, 24, 3, 8, 2, 4, 4, 1}
		shift = 2
	case 60:
		factors = []int{5, 12, 3, 4, 4, 1}
		shift = 3
	default:
		return nil
	}

	if bitrev == nil {
		bitrev = make([]int, nfft)
		computeBitrevTableRecursive(0, bitrev, 0, 1, 1, factors)
	}
	if twiddles == nil {
		twiddles = fftTwiddles48000_960Static[:]
	}

	maxFactors := len(factors) / 2
	fstride := make([]int, maxFactors+1)
	fstride[0] = 1
	for i := range maxFactors {
		p := factors[2*i]
		fstride[i+1] = fstride[i] * p
	}

	return &kissFFTState{
		nfft:        nfft,
		shift:       shift,
		factors:     factors,
		bitrev:      bitrev,
		w:           twiddles,
		fstride: fstride,
		stageTw: newKissStageTwiddles(factors, fstride, shift, twiddles),
	}
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
	// celt/kiss_fft.c:compute_twiddles evaluates phase and libm in double,
	// then kf_cexp narrows each stored component to opus_val16 (float).
	const pi = 3.14159265358979323846264338327
	for i := range nfft {
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
		for j := range p {
			if fIdx >= 0 && fIdx < len(bitrev) {
				bitrev[fIdx] = fout + j
			}
			fIdx += step
		}
		return
	}
	// Recursive level: call p times, advancing fIdx by step after each
	for range p {
		computeBitrevTableRecursive(fout, bitrev, fIdx, fstride*p, inStride, factors)
		fIdx += step
		fout += m
	}
}

func kfBfly2M1Available() bool { return true }

// kfBfly2M1 handles the radix-2 m==1 hot path on interleaved {r, i} pairs.
func kfBfly2M1(fout []float32, n int) {
	if n <= 0 {
		return
	}
	// Slice to 4*n so the compiler proves buf[0..3] are in bounds without
	// a stride-4 counter; eliminates per-pair bounds checks in the inner loop.
	buf := fout[:n<<2]
	for len(buf) >= 4 {
		ar := buf[0]
		ai := buf[1]
		br := buf[2]
		bi := buf[3]
		buf[0] = ar + br
		buf[1] = ai + bi
		buf[2] = ar - br
		buf[3] = ai - bi
		buf = buf[4:]
	}
}

// kfBfly4M1 handles the radix-4 m==1 hot path.
func kfBfly4M1(fout []float32, n int) {
	if n <= 0 {
		return
	}
	kfBfly4M1Core(fout, n)
}

// kfBfly3M1 handles the radix-3 m==1 path.
func kfBfly3M1(fout []float32, tw []kissCpx, fstride, n, mm int) {
	if n <= 0 || mm <= 0 {
		return
	}
	last := 2*((n-1)*mm+2) + 1
	if last >= len(fout) || fstride >= len(tw) {
		return
	}
	epi3i := tw[fstride].i
	_ = fout[last] // BCE hint for base+0..5 accesses.
	mm2 := 2 * mm
	for i := range n {
		base := i * mm2
		a0r, a0i := fout[base], fout[base+1]
		a1r, a1i := fout[base+2], fout[base+3]
		a2r, a2i := fout[base+4], fout[base+5]

		s3r := a1r + a2r
		s3i := a1i + a2i
		s0r := a1r - a2r
		s0i := a1i - a2i

		f1r := kissHalfSub(a0r, s3r)
		f1i := kissHalfSub(a0i, s3i)
		f0r := a0r + s3r
		f0i := a0i + s3i

		f1r, f1i, f2r, f2i := kissRadix3ScaledOutputs(f1r, f1i, s0r, s0i, epi3i)

		fout[base], fout[base+1] = f0r, f0i
		fout[base+2], fout[base+3] = f1r, f1i
		fout[base+4], fout[base+5] = f2r, f2i
	}
}

// kfBfly5M1 handles the radix-5 m==1 path.
func kfBfly5M1(fout []float32, tw []kissCpx, fstride, n, mm int) {
	if n <= 0 || mm <= 0 {
		return
	}
	last := 2*((n-1)*mm+4) + 1
	if last >= len(fout) || 2*fstride >= len(tw) {
		return
	}
	ya := tw[fstride]
	yb := tw[2*fstride]
	yar, yai := ya.r, ya.i
	ybr, ybi := yb.r, yb.i
	_ = fout[last] // BCE hint for base+0..9 accesses.
	mm2 := 2 * mm
	for i := range n {
		base := i * mm2
		a0r, a0i := fout[base], fout[base+1]
		a1r, a1i := fout[base+2], fout[base+3]
		a2r, a2i := fout[base+4], fout[base+5]
		a3r, a3i := fout[base+6], fout[base+7]
		a4r, a4i := fout[base+8], fout[base+9]

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

		fout[base], fout[base+1] = f0r, f0i
		fout[base+2], fout[base+3] = f1r, f1i
		fout[base+4], fout[base+5] = f2r, f2i
		fout[base+6], fout[base+7] = f3r, f3i
		fout[base+8], fout[base+9] = f4r, f4i
	}
}

func kfBfly2(fout []float32, m, N int) {
	if m == 1 {
		kfBfly2M1(fout, N)
		return
	}
	// m==4 degenerate radix-2 after radix-4
	kfBfly2M4(fout, N)
}

// kfBfly2M4Twiddle is the kf_bfly2 m == 4 twiddle, 0.7071067812.
const kfBfly2M4Twiddle = float32(0.7071067812)

// kfBfly2M4Scalar is the kf_bfly2 radix-2 stage with m == 4 (after a radix-4
// stage): N groups of eight interleaved complex values, each addressed through
// a fixed-size array view.
func kfBfly2M4Scalar(fout []float32, N int) {
	tw := kfBfly2M4Twiddle
	if N <= 0 {
		return
	}
	groups := fout[:16*N]
	for i := 0; i < len(groups); i += 16 {
		g := (*[16]float32)(groups[i : i+16])
		tr, ti := g[8], g[9]
		g[8] = g[0] - tr
		g[9] = g[1] - ti
		g[0] += tr
		g[1] += ti

		b1r, b1i := g[10], g[11]
		g[10], g[2] = kissBfly2M4Outputs(g[2], kissAdd(b1r, b1i), tw)
		g[11], g[3] = kissBfly2M4Outputs(g[3], kissSub(b1i, b1r), tw)

		tr = g[13]
		ti = -g[12]
		g[12] = kissSub(g[4], tr)
		g[13] = kissSub(g[5], ti)
		g[4] = kissAdd(g[4], tr)
		g[5] = kissAdd(g[5], ti)

		b3r, b3i := g[14], g[15]
		g[14], g[6] = kissBfly2M4Outputs(g[6], kissSub(b3i, b3r), tw)
		g[15], g[7] = kissBfly2M4Outputs(g[7], -kissAdd(b3i, b3r), tw)
	}
}

func kfBfly4(fout []float32, fstride int, st *kissFFTState, stage, m, N, mm int, fast bool) {
	if m == 1 {
		kfBfly4M1(fout, N)
		return
	}
	if N <= 0 || mm <= 0 {
		return
	}
	if fast {
		kfBfly4InnerFast(fout, st.stageTw[stage].tw4, N, mm)
		return
	}
	kfBfly4Inner(fout, st.w, m, N, mm, fstride)
}

func kfBfly3(fout []float32, fstride int, st *kissFFTState, stage, m, N, mm int, fast bool) {
	if N <= 0 || mm <= 0 {
		return
	}
	if m == 1 {
		kfBfly3M1(fout, st.w, fstride, N, mm)
		return
	}
	if fast {
		kfBfly3InnerFast(fout, st.stageTw[stage].tw3, st.w[fstride*m].i, N, mm)
		return
	}
	kfBfly3Inner(fout, st.w, m, N, mm, fstride)
}

func kfBfly5(fout []float32, fstride int, st *kissFFTState, stage, m, N, mm int, fast bool) {
	if N <= 0 || mm <= 0 {
		return
	}
	if m == 1 {
		kfBfly5M1(fout, st.w, fstride, N, mm)
		return
	}
	if fast {
		kfBfly5InnerFast(fout, st.stageTw[stage].tw5, st.w[fstride*m], st.w[fstride*2*m], N, mm)
		return
	}
	kfBfly5Inner(fout, st.w, m, N, mm, fstride)
}

func (st *kissFFTState) fftImpl(fout []float32) {
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
	for 2*L+1 < len(st.factors) {
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
	shift := max(st.shift, 0)
	fast := kfBflyScalarFastInput(fout)
	for i := L - 1; i >= 0; i-- {
		m2 := 1
		if i != 0 {
			m2 = st.factors[2*i-1]
		}
		twFstride := fstride[i] << shift
		N := fstride[i]
		switch st.factors[2*i] {
		case 2:
			kfBfly2(fout, m, N)
		case 4:
			kfBfly4(fout, twFstride, st, i, m, N, m2, fast)
		case 3:
			kfBfly3(fout, twFstride, st, i, m, N, m2, fast)
		case 5:
			kfBfly5(fout, twFstride, st, i, m, N, m2, fast)
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
	tmp := make([]float32, 2*n)
	kissFFT32To(out, x, tmp)
	return out
}

// KissFFT32ToWithScratch performs a forward complex FFT into out using caller-
// provided interleaved float32 scratch. scratch should have length >= 2*len(x)
// to avoid allocations.
func KissFFT32ToWithScratch(out []complex64, x []complex64, scratch []float32) {
	kissFFT32To(out, x, scratch)
}

// KissFFT32ToScaledWithScratch mirrors libopus opus_fft_c: it applies the
// kiss_fft_state scale while bit-reversing input into the interleaved float32
// FFT scratch (length >= 2*len(x) to avoid allocations).
func KissFFT32ToScaledWithScratch(out []complex64, x []complex64, scale float32, scratch []float32) {
	kissFFT32ToScaled(out, x, scale, scratch)
}

// KissFFT32ToScaledInterleaved mirrors libopus opus_fft_c and writes output
// directly as interleaved real/imag float32 pairs into outRI: [re0, im0, ...].
// outRI must have length >= 2*len(x).
func KissFFT32ToScaledInterleaved(outRI []float32, x []complex64, scale float32) {
	n := len(x)
	if n == 0 || len(outRI) < 2*n {
		return
	}
	_ = kissFFT32ToScaledScratch(x, scale, outRI[:2*n])
}

func kissFFT32ToScratch(x []complex64, scratch []float32, st *kissFFTState) []float32 {
	n := len(x)
	if n == 0 {
		return nil
	}
	n2 := 2 * n

	if st == nil {
		st = getKissFFTState(n)
	}
	if st == nil || len(st.bitrev) != n {
		tmp := make([]complex64, n)
		dft32FallbackTo(tmp, x)
		if len(scratch) < n2 {
			scratch = make([]float32, n2)
		}
		_ = scratch[n2-1]
		for i := range n {
			v := tmp[i]
			scratch[2*i] = real(v)
			scratch[2*i+1] = imag(v)
		}
		return scratch[:n2]
	}

	if len(scratch) < n2 {
		scratch = make([]float32, n2)
	}

	bitrev := st.bitrev
	_ = x[n-1]
	_ = bitrev[n-1]
	_ = scratch[n2-1]
	for i := range n {
		v := x[i]
		p := 2 * bitrev[i]
		scratch[p] = real(v)
		scratch[p+1] = imag(v)
	}

	st.fftImpl(scratch[:n2])
	return scratch[:n2]
}

func kissFFT32InterleavedToScratch(xRI []float32, scratch []float32, st *kissFFTState) []float32 {
	n := len(xRI) / 2
	if n == 0 {
		return nil
	}
	n2 := 2 * n

	if st == nil {
		st = getKissFFTState(n)
	}
	if st == nil || len(st.bitrev) != n {
		tmpIn := make([]complex64, n)
		tmpOut := make([]complex64, n)
		for i := range n {
			tmpIn[i] = complex(xRI[2*i], xRI[2*i+1])
		}
		dft32FallbackTo(tmpOut, tmpIn)
		if len(scratch) < n2 {
			scratch = make([]float32, n2)
		}
		_ = scratch[n2-1]
		for i := range n {
			v := tmpOut[i]
			scratch[2*i] = real(v)
			scratch[2*i+1] = imag(v)
		}
		return scratch[:n2]
	}

	if len(scratch) < n2 {
		scratch = make([]float32, n2)
	}

	bitrev := st.bitrev
	_ = xRI[n2-1]
	_ = bitrev[n-1]
	_ = scratch[n2-1]
	for i := range n {
		p := 2 * bitrev[i]
		scratch[p] = xRI[2*i]
		scratch[p+1] = xRI[2*i+1]
	}

	st.fftImpl(scratch[:n2])
	return scratch[:n2]
}

func kissFFT32ToScaledScratch(x []complex64, scale float32, scratch []float32) []float32 {
	n := len(x)
	if n == 0 {
		return nil
	}
	n2 := 2 * n

	st := getKissFFTState(n)
	if st == nil || len(st.bitrev) != n {
		tmpIn := make([]complex64, n)
		tmpOut := make([]complex64, n)
		for i := range n {
			tmpIn[i] = x[i] * complex(scale, 0)
		}
		dft32FallbackTo(tmpOut, tmpIn)
		if len(scratch) < n2 {
			scratch = make([]float32, n2)
		}
		_ = scratch[n2-1]
		for i := range n {
			v := tmpOut[i]
			scratch[2*i] = real(v)
			scratch[2*i+1] = imag(v)
		}
		return scratch[:n2]
	}

	if len(scratch) < n2 {
		scratch = make([]float32, n2)
	}

	bitrev := st.bitrev
	_ = x[n-1]
	_ = bitrev[n-1]
	_ = scratch[n2-1]
	for i := range n {
		v := x[i]
		p := 2 * bitrev[i]
		scratch[p] = real(v) * scale
		scratch[p+1] = imag(v) * scale
	}

	st.fftImpl(scratch[:n2])
	return scratch[:n2]
}

// kissFFT32To performs the Kiss FFT into a caller-provided output buffer.
// scratch must have length at least 2*len(x) to avoid allocations.
func kissFFT32To(out []complex64, x []complex64, scratch []float32) {
	n := len(x)
	if n == 0 || len(out) < n {
		return
	}
	scratch = kissFFT32ToScratch(x, scratch, nil)
	if len(scratch) < 2*n {
		return
	}

	// Convert back to complex64
	_ = out[n-1]       // BCE hint
	_ = scratch[2*n-1] // BCE hint
	for i := range n {
		out[i] = complex(scratch[2*i], scratch[2*i+1])
	}
}

func kissFFT32ToScaled(out []complex64, x []complex64, scale float32, scratch []float32) {
	n := len(x)
	if n == 0 || len(out) < n {
		return
	}
	scratch = kissFFT32ToScaledScratch(x, scale, scratch)
	if len(scratch) < 2*n {
		return
	}

	_ = out[n-1]
	_ = scratch[2*n-1]
	for i := range n {
		out[i] = complex(scratch[2*i], scratch[2*i+1])
	}
}

// kissFFT32ToInterleaved performs the Kiss FFT and writes output as interleaved
// real/imag float32 pairs into outRI: [re0, im0, re1, im1, ...].
// outRI must have length at least 2*len(x).
func kissFFT32ToInterleaved(outRI []float32, x []complex64, scratch []float32) {
	n := len(x)
	if n == 0 || len(outRI) < 2*n {
		return
	}
	_ = kissFFT32ToScratch(x, outRI[:2*n], nil)
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
	twoPi := float32(-6.2831855) / float32(n)
	for k := range n {
		angle := twoPi * float32(k)
		wStep := complex(opusmath.CosF32(angle), opusmath.SinF32(angle))
		w := complex(float32(1.0), float32(0.0))
		var sum complex64
		for t := range n {
			sum += x[t] * w
			w *= wStep
		}
		out[k] = sum
	}
}
