// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the forward MDCT transform for encoding.

package celt

import (
	"math"
)

// MDCT computes the forward Modified Discrete Cosine Transform.
// This is the transpose of IMDCT, converting time-domain samples to
// frequency-domain coefficients.
//
// Input: 2*N time samples (pre-windowed)
// Output: N frequency coefficients
//
// Formula: X[k] = sum_{n=0}^{2N-1} x[n] * cos(pi/N * (n + 0.5 + N/2) * (k + 0.5))
//
// The MDCT produces half as many coefficients as input samples. Combined with
// overlap-add, this achieves perfect reconstruction (MDCT -> IMDCT round-trip).
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/mdct.c
func MDCT(samples []float64) []float64 {
	if len(samples) == 0 {
		return nil
	}

	// Input is 2*N samples, output is N coefficients
	N2 := len(samples)
	N := N2 / 2

	if N <= 0 {
		return nil
	}

	// Apply Vorbis window to input samples
	windowed := make([]float64, N2)
	copy(windowed, samples)
	applyMDCTWindow(windowed)

	// Compute MDCT using direct formula
	// X[k] = sum_{n=0}^{2N-1} x[n] * cos(pi/N * (n + 0.5 + N/2) * (k + 0.5))
	coeffs := make([]float64, N)

	for k := 0; k < N; k++ {
		var sum float64
		kPlus := float64(k) + 0.5
		for n := 0; n < N2; n++ {
			nPlus := float64(n) + 0.5 + float64(N)/2
			angle := math.Pi / float64(N) * nPlus * kPlus
			sum += windowed[n] * math.Cos(angle)
		}
		coeffs[k] = sum
	}

	return coeffs
}

// MDCTShort computes the forward MDCT for transient frames with multiple short blocks.
// This processes multiple short MDCTs and interleaves the coefficients in the same
// format expected by IMDCTShort.
//
// samples: interleaved time samples for shortBlocks MDCTs
// shortBlocks: number of short MDCTs (2, 4, or 8)
// Returns: interleaved frequency coefficients
//
// In transient mode, CELT uses multiple shorter MDCTs instead of one long MDCT.
// This provides better time resolution for transients at the cost of reduced
// frequency resolution.
//
// Reference: libopus celt/celt_encoder.c, transient mode handling
func MDCTShort(samples []float64, shortBlocks int) []float64 {
	if shortBlocks <= 1 {
		return MDCT(samples)
	}

	totalSamples := len(samples)
	if totalSamples == 0 {
		return nil
	}

	// Each short block processes (totalSamples / shortBlocks) samples
	// and produces half that many coefficients
	shortSampleSize := totalSamples / shortBlocks
	shortCoeffSize := shortSampleSize / 2

	if shortSampleSize <= 0 || shortCoeffSize <= 0 {
		return MDCT(samples)
	}

	// Total output coefficients
	totalCoeffs := shortCoeffSize * shortBlocks
	output := make([]float64, totalCoeffs)

	// Process each short block
	for b := 0; b < shortBlocks; b++ {
		// Extract samples for this short block
		shortSamples := make([]float64, shortSampleSize)
		startIdx := b * shortSampleSize
		for i := 0; i < shortSampleSize && startIdx+i < totalSamples; i++ {
			shortSamples[i] = samples[startIdx+i]
		}

		// Compute MDCT for this short block
		shortCoeffs := mdctDirect(shortSamples)

		// Interleave coefficients: coeff[b + i*shortBlocks]
		for i := 0; i < len(shortCoeffs) && i < shortCoeffSize; i++ {
			outIdx := b + i*shortBlocks
			if outIdx < totalCoeffs {
				output[outIdx] = shortCoeffs[i]
			}
		}
	}

	return output
}

// mdctDirect computes MDCT without windowing (assumes pre-windowed input).
// Used by MDCTShort for individual short blocks.
func mdctDirect(samples []float64) []float64 {
	N2 := len(samples)
	N := N2 / 2

	if N <= 0 {
		return nil
	}

	coeffs := make([]float64, N)

	for k := 0; k < N; k++ {
		var sum float64
		kPlus := float64(k) + 0.5
		for n := 0; n < N2; n++ {
			nPlus := float64(n) + 0.5 + float64(N)/2
			angle := math.Pi / float64(N) * nPlus * kPlus
			sum += samples[n] * math.Cos(angle)
		}
		coeffs[k] = sum
	}

	return coeffs
}

// applyMDCTWindow applies the Vorbis window to samples for MDCT analysis.
// CELT uses short overlap (120 samples) rather than 50% overlap.
// Only the first and last 'overlap' samples are windowed; middle samples are unmodified.
func applyMDCTWindow(samples []float64) {
	n := len(samples)
	if n <= 0 {
		return
	}

	// CELT uses short overlap of 120 samples
	overlap := Overlap
	if overlap > n/2 {
		overlap = n / 2
	}

	// Get precomputed window for overlap region
	window := GetWindowBuffer(overlap)

	// Apply window to beginning (rising edge) - first 'overlap' samples
	for i := 0; i < overlap && i < n; i++ {
		samples[i] *= window[i]
	}

	// Middle samples are unmodified (window = 1.0)

	// Apply window to end (falling edge) - last 'overlap' samples
	for i := 0; i < overlap && n-overlap+i < n; i++ {
		idx := n - overlap + i
		// Falling edge uses window in reverse: window[overlap-1-i]
		samples[idx] *= window[overlap-1-i]
	}
}
