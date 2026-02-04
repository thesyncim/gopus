package celt

import (
	"math"
	"math/cmplx"
	"sync"
)

// IMDCT (Inverse Modified Discrete Cosine Transform) implementation for CELT.
// This file provides FFT-based IMDCT for efficient frequency-to-time conversion.
//
// The IMDCT is the core synthesis transform in CELT, converting frequency-domain
// MDCT coefficients back to time-domain audio samples. Using FFT reduces complexity
// from O(n^2) to O(n log n).
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/mdct.c

// mdctTwiddles contains precomputed twiddle factors for IMDCT.
// Key: MDCT size (number of frequency bins)
// Generated lazily for each supported size.
var mdctTwiddles = make(map[int]*mdctTwiddleSet)

// mdctTwiddleSet holds precomputed twiddles for a specific IMDCT size.
type mdctTwiddleSet struct {
	n      int          // Number of frequency bins
	preTw  []complex128 // Pre-twiddle factors
	postTw []complex128 // Post-twiddle factors
	fftTw  []complex128 // FFT twiddle factors
}

// initMDCTTwiddles initializes twiddle factors for a given IMDCT size.
func initMDCTTwiddles(n int) *mdctTwiddleSet {
	if tw, ok := mdctTwiddles[n]; ok {
		return tw
	}

	tw := &mdctTwiddleSet{
		n:      n,
		preTw:  make([]complex128, n/2),
		postTw: make([]complex128, n/2),
	}

	n2 := n * 2 // Output size
	n4 := n / 2 // FFT size

	// Pre-twiddle: exp(-j * pi * (k + 0.5 + n/2) / n)
	for k := 0; k < n4; k++ {
		angle := -math.Pi * (float64(k) + 0.5 + float64(n)/2) / float64(n)
		tw.preTw[k] = complex(math.Cos(angle), math.Sin(angle))
	}

	// Post-twiddle: exp(-j * pi * (n + 0.5) * (2*k + 1) / n2)
	for k := 0; k < n4; k++ {
		angle := -math.Pi * (float64(n) + 0.5) * (2*float64(k) + 1) / float64(n2)
		tw.postTw[k] = complex(math.Cos(angle), math.Sin(angle))
	}

	// FFT twiddle factors for size n4
	tw.fftTw = make([]complex128, n4)
	for k := 0; k < n4; k++ {
		angle := -2.0 * math.Pi * float64(k) / float64(n4)
		tw.fftTw[k] = complex(math.Cos(angle), math.Sin(angle))
	}

	mdctTwiddles[n] = tw
	return tw
}

var (
	imdctCosMu    sync.Mutex
	imdctCosCache = map[int][]float64{}
)

var (
	mdctTrigMu    sync.Mutex
	mdctTrigCache = map[int][]float64{}
)

var (
	mdctTrigMuF32    sync.Mutex
	mdctTrigCacheF32 = map[int][]float32{}
)

func getMDCTTrig(n int) []float64 {
	mdctTrigMu.Lock()
	defer mdctTrigMu.Unlock()

	if trig, ok := mdctTrigCache[n]; ok {
		return trig
	}

	n2 := n / 2
	trig := make([]float64, n2)
	for i := 0; i < n2; i++ {
		// Twiddle factor for IMDCT pre/post rotation
		// Formula: cos(2*π*(i+0.125)/n) where n is the output size (2*N)
		angle := 2.0 * math.Pi * (float64(i) + 0.125) / float64(n)
		trig[i] = math.Cos(angle)
	}

	mdctTrigCache[n] = trig
	return trig
}

func getMDCTTrigF32(n int) []float32 {
	mdctTrigMuF32.Lock()
	defer mdctTrigMuF32.Unlock()

	if trig, ok := mdctTrigCacheF32[n]; ok {
		return trig
	}

	n2 := n / 2
	trig := make([]float32, n2)
	for i := 0; i < n2; i++ {
		angle := 2.0 * math.Pi * (float64(i) + 0.125) / float64(n)
		trig[i] = float32(math.Cos(angle))
	}

	mdctTrigCacheF32[n] = trig
	return trig
}

func getIMDCTCosTable(n int) []float64 {
	imdctCosMu.Lock()
	defer imdctCosMu.Unlock()

	if table, ok := imdctCosCache[n]; ok {
		return table
	}

	n2 := n * 2
	table := make([]float64, n2*n)
	base := math.Pi / float64(n)
	nHalf := float64(n) / 2.0
	for i := 0; i < n2; i++ {
		nTerm := float64(i) + 0.5 + nHalf
		row := table[i*n : (i+1)*n]
		for k := 0; k < n; k++ {
			angle := base * nTerm * (float64(k) + 0.5)
			row[k] = math.Cos(angle)
		}
	}

	imdctCosCache[n] = table
	return table
}

// IMDCT computes the inverse MDCT of frequency coefficients.
// Input: n frequency bins (spectrum)
// Output: 2*n time samples
//
// For power-of-2 sizes, uses FFT-based approach for O(n log n) complexity.
// For other sizes (like CELT's 120, 240, 480, 960), uses direct computation
// which is O(n^2) but handles any size correctly.
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/mdct.c
func IMDCT(spectrum []float64) []float64 {
	n := len(spectrum)
	if n <= 0 {
		return nil
	}

	n2 := n * 2 // Output size
	n4 := n / 2 // FFT size

	// Handle edge case for very small sizes
	if n4 < 1 {
		// Direct computation for n=1
		out := make([]float64, n2)
		out[0] = spectrum[0]
		out[1] = spectrum[0]
		return out
	}

	// Check if n4 is a power of 2 for FFT-based IMDCT
	if isPowerOfTwo(n4) {
		return imdctFFT(spectrum, n, n2, n4)
	}

	// Fall back to direct computation for non-power-of-2 sizes
	return IMDCTDirect(spectrum)
}

// IMDCTOverlap computes the CELT IMDCT with short overlap using a zero
// previous overlap buffer. Output length is N + overlap.
func IMDCTOverlap(spectrum []float64, overlap int) []float64 {
	if len(spectrum) == 0 {
		return nil
	}
	prev := make([]float64, overlap)
	return imdctOverlapWithPrev(spectrum, prev, overlap)
}

// IMDCTOverlapWithPrev computes CELT IMDCT using the provided overlap history.
// The returned slice includes frameSize+overlap samples.
func IMDCTOverlapWithPrev(spectrum, prevOverlap []float64, overlap int) []float64 {
	if len(spectrum) == 0 {
		return nil
	}
	if prevOverlap == nil {
		prevOverlap = make([]float64, overlap)
	}
	return imdctOverlapWithPrev(spectrum, prevOverlap, overlap)
}

func imdctOverlapWithPrev(spectrum []float64, prevOverlap []float64, overlap int) []float64 {
	n2 := len(spectrum)
	if n2 == 0 {
		return nil
	}
	if overlap < 0 {
		overlap = 0
	}

	out := make([]float64, n2+overlap)
	imdctOverlapWithPrevScratch(out, spectrum, prevOverlap, overlap, nil)
	return out
}

func imdctOverlapWithPrevScratch(out []float64, spectrum []float64, prevOverlap []float64, overlap int, scratch *imdctScratch) {
	n2 := len(spectrum)
	if n2 == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}

	n := n2 * 2
	n4 := n2 / 2
	needed := n2 + overlap
	if len(out) < needed {
		return
	}
	// Clear output; preserves no stale data.
	for i := 0; i < needed; i++ {
		out[i] = 0
	}

	// Copy the full prevOverlap to out[0:overlap].
	// The IMDCT will overwrite out[overlap/2:...], but the TDAC needs
	// out[0:overlap/2] from prevOverlap.
	if overlap > 0 && len(prevOverlap) > 0 {
		copyLen := min(len(prevOverlap), overlap)
		copy(out[:copyLen], prevOverlap[:copyLen])
	}

	trig := getMDCTTrig(n)

	var fftIn []complex128
	var fftOut []complex128
	var buf []float64
	if scratch == nil {
		fftIn = make([]complex128, n4)
		fftOut = make([]complex128, n4)
		buf = make([]float64, n2)
	} else {
		fftIn = ensureComplexSlice(&scratch.fftIn, n4)
		fftOut = ensureComplexSlice(&scratch.fftOut, n4)
		buf = ensureFloat64Slice(&scratch.buf, n2)
	}
	for i := 0; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1
		// Swap real/imag because we use an FFT instead of an IFFT.
		fftIn[i] = complex(yi, yr)
	}

	dftTo(fftOut, fftIn)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		buf[2*i] = real(v)
		buf[2*i+1] = imag(v)
	}

	yp0 := 0
	yp1 := n2 - 2
	for i := 0; i < (n4+1)>>1; i++ {
		re := buf[yp0+1]
		im := buf[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0
		re2 := buf[yp1+1]
		im2 := buf[yp1]
		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0
		buf[yp1] = yr
		buf[yp0+1] = yi
		yp0 += 2
		yp1 -= 2
	}

	// Copy IMDCT output to out, starting at overlap/2.
	// This leaves out[0:overlap/2] with prevOverlap data for TDAC.
	start := overlap / 2
	if start+n2 <= len(out) {
		copy(out[start:start+n2], buf)
	}

	// TDAC windowing blends out[0:overlap]
	if overlap > 0 {
		window := GetWindowBuffer(overlap)
		xp1 := overlap - 1
		yp1 := 0
		wp1 := 0
		wp2 := overlap - 1
		for i := 0; i < overlap/2; i++ {
			x1 := out[xp1]
			x2 := out[yp1]
			out[yp1] = x2*window[wp2] - x1*window[wp1]
			out[xp1] = x2*window[wp1] + x1*window[wp2]
			yp1++
			xp1--
			wp1++
			wp2--
		}
	}

	// The output now has:
	// - out[0:overlap] = TDAC windowed region
	// - out[overlap:n2+overlap/2] = IMDCT output
	// - out[n2+overlap/2:n2+overlap] = zeros (initialized by make)
	//
	// The caller extracts out[n2:n2+overlap] for the next frame's overlap:
	// - out[n2:n2+overlap/2] = last overlap/2 samples of IMDCT (for next TDAC's prev)
	// - out[n2+overlap/2:n2+overlap] = zeros (will be overwritten by next IMDCT's first overlap/2)
	//
	// This is correct - the zeros will be replaced during the next frame's IMDCT.
	// Output is already in out.
}

// imdctOverlapWithPrevScratchF32 performs IMDCT using float32 precision to match libopus.
// This is used for long (non-transient) blocks.
func imdctOverlapWithPrevScratchF32(out []float64, spectrum []float64, prevOverlap []float64, overlap int, scratch *imdctScratchF32) {
	n2 := len(spectrum)
	if n2 == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}

	n := n2 * 2
	n4 := n2 / 2
	needed := n2 + overlap
	start := overlap / 2
	if len(out) < needed {
		return
	}
	// Use float32 trig table to match libopus
	trig := getMDCTTrigF32(n)

	var fftIn []complex64
	var fftOut []complex64
	var buf []float32
	var fftTmp []kissCpx
	var outF32 []float32
	if scratch == nil {
		fftIn = make([]complex64, n4)
		fftOut = make([]complex64, n4)
		fftTmp = make([]kissCpx, n4)
		buf = make([]float32, n2)
		outF32 = make([]float32, needed)
	} else {
		fftIn = ensureComplex64Slice(&scratch.fftIn, n4)
		fftOut = ensureComplex64Slice(&scratch.fftOut, n4)
		fftTmp = ensureKissCpxSlice(&scratch.fftTmp, n4)
		buf = ensureFloat32Slice(&scratch.buf, n2)
		outF32 = ensureFloat32Slice(&scratch.out, needed)
	}

	// Clear only the regions that must be zeroed when reusing scratch.
	// The IMDCT output overwrites [start:start+n2], and prevOverlap overwrites [0:overlap].
	// Only the tail [start+n2:needed] must be zeroed each call.
	if start+n2 < needed {
		clear(outF32[start+n2 : needed])
	}

	// Copy the full prevOverlap to outF32[0:overlap].
	if overlap > 0 && len(prevOverlap) > 0 {
		copyLen := min(len(prevOverlap), overlap)
		for i := 0; i < copyLen; i++ {
			outF32[i] = float32(prevOverlap[i])
		}
		if copyLen < overlap {
			clear(outF32[copyLen:overlap])
		}
	} else if overlap > 0 {
		clear(outF32[:overlap])
	}

	imdctPreRotateF32(fftIn, spectrum, trig, n2, n4)

	kissFFT32To(fftOut, fftIn, fftTmp)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		buf[2*i] = real(v)
		buf[2*i+1] = imag(v)
	}

	imdctPostRotateF32(buf, trig, n2, n4)

	// Copy IMDCT output to outF32, starting at overlap/2
	if start+n2 <= len(outF32) {
		copy(outF32[start:start+n2], buf)
	}

	// TDAC windowing blends outF32[0:overlap] using float32
	if overlap > 0 {
		windowF32 := GetWindowBufferF32(overlap)
		xp1 := overlap - 1
		yp1 := 0
		wp1 := 0
		wp2 := overlap - 1
		limit := overlap / 2
		i := 0
		for ; i+1 < limit; i += 2 {
			x1 := outF32[xp1]
			x2 := outF32[yp1]
			outF32[yp1] = x2*windowF32[wp2] - x1*windowF32[wp1]
			outF32[xp1] = x2*windowF32[wp1] + x1*windowF32[wp2]
			yp1++
			xp1--
			wp1++
			wp2--

			x1 = outF32[xp1]
			x2 = outF32[yp1]
			outF32[yp1] = x2*windowF32[wp2] - x1*windowF32[wp1]
			outF32[xp1] = x2*windowF32[wp1] + x1*windowF32[wp2]
			yp1++
			xp1--
			wp1++
			wp2--
		}
		for ; i < limit; i++ {
			x1 := outF32[xp1]
			x2 := outF32[yp1]
			outF32[yp1] = x2*windowF32[wp2] - x1*windowF32[wp1]
			outF32[xp1] = x2*windowF32[wp1] + x1*windowF32[wp2]
			yp1++
			xp1--
			wp1++
			wp2--
		}
	}

	if needed > 0 {
		out = out[:needed:needed]
		outF32 = outF32[:needed:needed]
		_ = outF32[needed-1]
	}
	i := 0
	for ; i+3 < needed; i += 4 {
		out[i] = float64(outF32[i])
		out[i+1] = float64(outF32[i+1])
		out[i+2] = float64(outF32[i+2])
		out[i+3] = float64(outF32[i+3])
	}
	for ; i < needed; i++ {
		out[i] = float64(outF32[i])
	}
}

// imdctInPlace performs IMDCT directly into a shared output buffer at the given offset.
// This matches libopus's clt_mdct_backward behavior for short block processing.
//
// The function writes to out[blockStart:blockStart+n2+overlap/2], where n2 = len(spectrum).
// The TDAC windowing blends out[blockStart:blockStart+overlap] with existing data.
//
// Parameters:
//   - spectrum: MDCT coefficients for this short block
//   - out: shared output buffer that already contains previous block/frame data
//   - blockStart: starting position in out for this block
//   - overlap: overlap size (typically 120 for CELT at 48kHz)
func imdctInPlace(spectrum []float64, out []float64, blockStart, overlap int) {
	imdctInPlaceScratch(spectrum, out, blockStart, overlap, nil)
}

func imdctInPlaceScratch(spectrum []float64, out []float64, blockStart, overlap int, scratch *imdctScratch) {
	n2 := len(spectrum)
	if n2 == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}

	n := n2 * 2
	n4 := n2 / 2

	trig := getMDCTTrig(n)

	// Pre-rotate with twiddles
	var fftIn []complex128
	var fftOut []complex128
	var buf []float64
	if scratch == nil {
		fftIn = make([]complex128, n4)
		fftOut = make([]complex128, n4)
		buf = make([]float64, n2)
	} else {
		fftIn = ensureComplexSlice(&scratch.fftIn, n4)
		fftOut = ensureComplexSlice(&scratch.fftOut, n4)
		buf = ensureFloat64Slice(&scratch.buf, n2)
	}
	for i := 0; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1
		fftIn[i] = complex(yi, yr)
	}

	// FFT
	dftTo(fftOut, fftIn)

	// Post-rotate
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		buf[2*i] = real(v)
		buf[2*i+1] = imag(v)
	}

	yp0 := 0
	yp1 := n2 - 2
	for i := 0; i < (n4+1)>>1; i++ {
		re := buf[yp0+1]
		im := buf[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0
		re2 := buf[yp1+1]
		im2 := buf[yp1]
		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0
		buf[yp1] = yr
		buf[yp0+1] = yi
		yp0 += 2
		yp1 -= 2
	}

	// Write IMDCT output to shared buffer starting at blockStart + overlap/2
	// This is the key difference from imdctOverlapWithPrev - we write directly
	// to the shared buffer, preserving whatever is in out[blockStart:blockStart+overlap/2]
	start := blockStart + overlap/2
	end := start + n2
	if end > len(out) {
		end = len(out)
	}
	for i := start; i < end; i++ {
		out[i] = buf[i-start]
	}

	// TDAC windowing blends out[blockStart:blockStart+overlap]
	// The first half (out[blockStart:blockStart+overlap/2]) contains previous data
	// The second half (out[blockStart+overlap/2:blockStart+overlap]) is IMDCT output
	if overlap > 0 {
		window := GetWindowBuffer(overlap)
		xp1 := blockStart + overlap - 1
		yp1 := blockStart
		wp1 := 0
		wp2 := overlap - 1
		for i := 0; i < overlap/2; i++ {
			x1 := out[xp1]
			x2 := out[yp1]
			out[yp1] = x2*window[wp2] - x1*window[wp1]
			out[xp1] = x2*window[wp1] + x1*window[wp2]
			yp1++
			xp1--
			wp1++
			wp2--
		}
	}
}

// ImdctInPlaceExported exports imdctInPlace for testing
func ImdctInPlaceExported(spectrum []float64, out []float64, blockStart, overlap int) {
	imdctInPlace(spectrum, out, blockStart, overlap)
}

// imdctInPlaceScratchF32 performs IMDCT using float32 precision to match libopus.
// This function is critical for matching libopus's floating-point behavior exactly.
// libopus uses float (32-bit) for IMDCT, so using float64 in gopus causes precision
// differences that accumulate, especially in transient frames with multiple short blocks.
func imdctInPlaceScratchF32(spectrum []float64, out []float64, blockStart, overlap int, scratch *imdctScratchF32) {
	n2 := len(spectrum)
	if n2 == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}

	n := n2 * 2
	n4 := n2 / 2

	// Use float32 trig table to match libopus
	trig := getMDCTTrigF32(n)

	// Pre-rotate with twiddles using float32
	var fftIn []complex64
	var fftOut []complex64
	var buf []float32
	var fftTmp []kissCpx
	if scratch == nil {
		fftIn = make([]complex64, n4)
		fftOut = make([]complex64, n4)
		fftTmp = make([]kissCpx, n4)
		buf = make([]float32, n2)
	} else {
		fftIn = ensureComplex64Slice(&scratch.fftIn, n4)
		fftOut = ensureComplex64Slice(&scratch.fftOut, n4)
		fftTmp = ensureKissCpxSlice(&scratch.fftTmp, n4)
		buf = ensureFloat32Slice(&scratch.buf, n2)
	}

	imdctPreRotateF32(fftIn, spectrum, trig, n2, n4)

	// FFT using float32 kiss_fft implementation
	kissFFT32To(fftOut, fftIn, fftTmp)

	// Post-rotate using float32
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		buf[2*i] = real(v)
		buf[2*i+1] = imag(v)
	}

	imdctPostRotateF32(buf, trig, n2, n4)

	start := blockStart + overlap/2
	if start >= len(out) {
		return
	}

	// TDAC windowing using float32 window
	if overlap > 0 {
		windowF32 := GetWindowBufferF32(overlap)
		xp1 := blockStart + overlap - 1
		yp1 := blockStart
		wp1 := 0
		wp2 := overlap - 1
		for i := 0; i < overlap/2; i++ {
			bufIdx := xp1 - start
			x1 := buf[bufIdx]
			x2 := float32(out[yp1])
			out[yp1] = float64(x2*windowF32[wp2] - x1*windowF32[wp1])
			out[xp1] = float64(x2*windowF32[wp1] + x1*windowF32[wp2])
			yp1++
			xp1--
			wp1++
			wp2--
		}
	}

	copyStart := 0
	if overlap > 0 {
		copyStart = overlap / 2
	}
	limit := n2
	if start+limit > len(out) {
		limit = len(out) - start
	}
	for i := copyStart; i < limit; i++ {
		out[start+i] = float64(buf[i])
	}
}

// isPowerOfTwo returns true if n is a power of 2.
func isPowerOfTwo(n int) bool {
	return n > 0 && (n&(n-1)) == 0
}

// imdctFFT computes IMDCT using FFT for power-of-2 sizes.
func imdctFFT(spectrum []float64, n, n2, n4 int) []float64 {
	// Get or compute twiddles
	tw := initMDCTTwiddles(n)

	// Step 1: Pre-twiddle and combine pairs
	// Combine X[k] and X[n-1-k] into complex values
	fftIn := make([]complex128, n4)
	for k := 0; k < n4; k++ {
		// Even index: k
		// Odd index: n-1-k
		xEven := spectrum[2*k]
		xOdd := spectrum[n-1-2*k]

		// Pre-twiddle: multiply by exp(-j * pi * (k + 0.5 + n/2) / n)
		fftIn[k] = complex(xEven, xOdd) * tw.preTw[k]
	}

	// Step 2: Compute n/4 point complex FFT (inverse)
	fftOut := ifft(fftIn)

	// Step 3: Post-twiddle and unfold to 2n output
	output := make([]float64, n2)

	for k := 0; k < n4; k++ {
		// Post-twiddle
		y := fftOut[k] * tw.postTw[k]

		// Scale factor for IMDCT normalization
		// The IMDCT normalization factor is 2/n
		scale := 2.0 / float64(n)

		yr := real(y) * scale
		yi := imag(y) * scale

		// Unfold using MDCT symmetry:
		// output[n/2 - 1 - 2k] = yr
		// output[n/2 + 2k] = yr
		// output[n + n/2 - 1 - 2k] = yi
		// output[n + n/2 + 2k] = -yi

		idx1 := n4 - 1 - k
		idx2 := n4 + k
		idx3 := n + n4 - 1 - k
		idx4 := n + n4 + k

		if idx1 >= 0 && idx1 < n2 {
			output[idx1] = yr
		}
		if idx2 >= 0 && idx2 < n2 {
			output[idx2] = yr
		}
		if idx3 >= 0 && idx3 < n2 {
			output[idx3] = yi
		}
		if idx4 >= 0 && idx4 < n2 {
			output[idx4] = -yi
		}
	}

	return output
}

// IMDCTShort computes IMDCT for transient frames with multiple short blocks.
// coeffs: interleaved coefficients for shortBlocks MDCTs
// shortBlocks: number of short MDCTs (2, 4, or 8)
// Returns: interleaved time samples with proper overlap handling.
//
// In transient mode, CELT uses multiple shorter MDCTs instead of one long MDCT.
// This provides better time resolution for transients (like drum hits) at the
// cost of reduced frequency resolution.
//
// Reference: libopus celt/celt_decoder.c, transient mode handling
func IMDCTShort(coeffs []float64, shortBlocks int) []float64 {
	if shortBlocks <= 1 {
		return IMDCT(coeffs)
	}

	totalCoeffs := len(coeffs)
	if totalCoeffs == 0 {
		return nil
	}

	// Each short block has totalCoeffs/shortBlocks coefficients
	shortSize := totalCoeffs / shortBlocks
	if shortSize <= 0 {
		return IMDCT(coeffs)
	}

	// Output: each short IMDCT produces 2*shortSize samples
	// With overlap, total output is shortSize*(shortBlocks+1)
	// But for simplicity, we produce 2*totalCoeffs and let caller handle overlap
	output := make([]float64, 2*totalCoeffs)

	// Process each short block
	for b := 0; b < shortBlocks; b++ {
		// Extract coefficients for this short block
		shortCoeffs := make([]float64, shortSize)
		for i := 0; i < shortSize; i++ {
			// Coefficients are interleaved: coeff[b + i*shortBlocks]
			srcIdx := b + i*shortBlocks
			if srcIdx < totalCoeffs {
				shortCoeffs[i] = coeffs[srcIdx]
			}
		}

		// Compute IMDCT for this short block
		shortOut := IMDCT(shortCoeffs)

		// Place output in interleaved fashion
		// Output position for this block
		outOffset := b * shortSize * 2
		for i := 0; i < len(shortOut) && outOffset+i < len(output); i++ {
			output[outOffset+i] = shortOut[i]
		}
	}

	return output
}

// fft computes the in-place complex FFT using Cooley-Tukey radix-2 algorithm.
// Uses decimation-in-time (DIT) approach.
func fft(x []complex128) []complex128 {
	n := len(x)
	if n <= 1 {
		return x
	}

	// Make a copy to avoid modifying input
	result := make([]complex128, n)
	copy(result, x)

	// Bit-reversal permutation
	bitReverse(result)

	// Cooley-Tukey iterative FFT
	for size := 2; size <= n; size *= 2 {
		halfSize := size / 2
		// Twiddle factor for this stage
		angle := -2.0 * math.Pi / float64(size)
		w := complex(math.Cos(angle), math.Sin(angle))

		for start := 0; start < n; start += size {
			wk := complex(1, 0)
			for k := 0; k < halfSize; k++ {
				idx1 := start + k
				idx2 := start + k + halfSize

				t := wk * result[idx2]
				result[idx2] = result[idx1] - t
				result[idx1] = result[idx1] + t

				wk *= w
			}
		}
	}

	return result
}

// ifft computes the inverse FFT.
func ifft(x []complex128) []complex128 {
	n := len(x)
	if n <= 1 {
		return x
	}

	// Conjugate input
	conj := make([]complex128, n)
	for i, v := range x {
		conj[i] = cmplx.Conj(v)
	}

	// Forward FFT
	result := fft(conj)

	// Conjugate and scale output
	scale := 1.0 / float64(n)
	for i := range result {
		result[i] = cmplx.Conj(result[i]) * complex(scale, 0)
	}

	return result
}

func dft(x []complex128) []complex128 {
	n := len(x)
	if n <= 1 {
		return x
	}

	out := make([]complex128, n)
	dftTo(out, x)
	return out
}

func dftTo(out []complex128, x []complex128) {
	n := len(x)
	if n <= 1 {
		if len(out) > 0 && len(x) > 0 {
			out[0] = x[0]
		}
		return
	}
	if len(out) < n {
		return
	}

	// Use mixed-radix FFT for O(n log n) complexity on supported sizes
	state := GetKissFFT64State(n)
	if state != nil {
		kissFFT64Forward(out, x, state)
		return
	}

	// Fall back to O(n^2) DFT for unsupported sizes
	twoPi := -2.0 * math.Pi / float64(n)
	for k := 0; k < n; k++ {
		angle := twoPi * float64(k)
		wStep := complex(math.Cos(angle), math.Sin(angle))
		w := complex(1.0, 0.0)
		var sum complex128
		for t := 0; t < n; t++ {
			sum += x[t] * w
			w *= wStep
		}
		out[k] = sum
	}
}

func dft32(x []complex64) []complex64 {
	n := len(x)
	if n <= 1 {
		return x
	}

	out := make([]complex64, n)
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
	return out
}

// bitReverse performs bit-reversal permutation on the input slice.
func bitReverse(x []complex128) {
	n := len(x)
	bits := 0
	for temp := n; temp > 1; temp >>= 1 {
		bits++
	}

	for i := 0; i < n; i++ {
		j := reverseBits(i, bits)
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
}

// reverseBits reverses the lower 'bits' bits of x.
func reverseBits(x, bits int) int {
	result := 0
	for i := 0; i < bits; i++ {
		result = (result << 1) | (x & 1)
		x >>= 1
	}
	return result
}

// IMDCTDirect computes IMDCT per RFC 6716 Section 4.3.5.
// Formula: y[n] = sum_{k=0}^{N-1} X[k] * cos(pi/N * (n + 0.5 + N/2) * (k + 0.5))
// Input: N frequency coefficients
// Output: 2*N time samples
// Normalization: matches libopus test_unit_mdct.c inverse (no extra scaling)
//
// This is O(n^2) but mathematically exact and handles non-power-of-2 sizes
// (like CELT's 120, 240, 480, 960) that the FFT-based approach cannot.
func IMDCTDirect(spectrum []float64) []float64 {
	N := len(spectrum)
	if N <= 0 {
		return nil
	}

	N2 := N * 2
	output := make([]float64, N2)
	table := getIMDCTCosTable(N)
	for n := 0; n < N2; n++ {
		var sum float64
		row := table[n*N : (n+1)*N]
		for k := 0; k < N; k++ {
			sum += spectrum[k] * row[k]
		}
		output[n] = sum
	}

	return output
}
