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
// Normalization: 2/N factor applied for proper amplitude
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
	scale := 2.0 / float64(N)

	for n := 0; n < N2; n++ {
		var sum float64
		row := table[n*N : (n+1)*N]
		for k := 0; k < N; k++ {
			sum += spectrum[k] * row[k]
		}
		output[n] = sum * scale
	}

	return output
}
