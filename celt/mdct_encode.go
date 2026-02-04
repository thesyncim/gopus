// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the forward MDCT transform for encoding.

package celt

import (
	"math"
)

// MDCT computes the forward Modified Discrete Cosine Transform.
// For CELT-typical inputs (frameSize+Overlap), this uses the short-overlap
// algorithm from libopus. For legacy 2*N inputs, it falls back to the
// direct MDCT formula.
func MDCT(samples []float64) []float64 {
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) {
			return mdctForwardOverlap(samples, Overlap)
		}
	}

	return mdctStandard(samples)
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
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) && frameSize%shortBlocks == 0 {
			return mdctForwardShortOverlap(samples, Overlap, shortBlocks)
		}
	}

	return mdctShortStandard(samples, shortBlocks)
}

// mdctCoreCompute computes the core MDCT formula into the provided coeffs slice.
// samples: input samples of length N2 (2*N)
// coeffs: output coefficients of length N
// scale: scale factor applied to each coefficient
// This is the shared implementation used by both mdctDirect and mdctStandard.
// Formula: X[k] = scale * sum_{n=0}^{N2-1} x[n] * cos(pi/N * (n+0.5+N/2) * (k+0.5))
func mdctCoreCompute(samples []float64, coeffs []float64, scale float64) {
	N2 := len(samples)
	N := N2 / 2
	if N <= 0 || len(coeffs) < N {
		return
	}

	for k := 0; k < N; k++ {
		var sum float64
		kPlus := float64(k) + 0.5
		for n := 0; n < N2; n++ {
			nPlus := float64(n) + 0.5 + float64(N)/2
			angle := math.Pi / float64(N) * nPlus * kPlus
			sum += samples[n] * math.Cos(angle)
		}
		coeffs[k] = sum * scale
	}
}

// mdctDirect computes MDCT without windowing (assumes pre-windowed input).
// Used by MDCTShort for individual short blocks.
// The output is scaled by 4/N2 (or equivalently 2/N) to match libopus normalization.
// Reference: libopus celt/tests/test_unit_mdct.c check() function
// Formula: X[k] = sum_{n=0}^{N2-1} x[n] * cos(2*pi*(n+0.5+0.25*N2)*(k+0.5)/N2) / (N2/4)
func mdctDirect(samples []float64) []float64 {
	N2 := len(samples)
	N := N2 / 2

	if N <= 0 {
		return nil
	}

	coeffs := make([]float64, N)

	// Scale factor: 4/N2 = 4/(2*N) = 2/N
	// This matches libopus's division by N/4 in the test formula
	scale := 4.0 / float64(N2)

	mdctCoreCompute(samples, coeffs, scale)

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

// MDCTForwardWithOverlap is the exported version of mdctForwardOverlap for testing.
// Input: samples with length frameSize+overlap
// Returns: MDCT coefficients of length frameSize
func MDCTForwardWithOverlap(samples []float64, overlap int) []float64 {
	return mdctForwardOverlap(samples, overlap)
}

// mdctForwardOverlap implements the CELT short-overlap MDCT (libopus clt_mdct_forward)
// for a single block. Input length must be frameSize+overlap.
// This uses float32 arithmetic internally to match libopus float precision.
func mdctForwardOverlap(samples []float64, overlap int) []float64 {
	return mdctForwardOverlapF32(samples, overlap)
}

// mdctForwardOverlapF32 is a float32-precision MDCT matching libopus float path.
func mdctForwardOverlapF32(samples []float64, overlap int) []float64 {
	coeffs := make([]float64, len(samples)-overlap)
	mdctForwardOverlapF32Scratch(samples, overlap, coeffs, nil, nil, nil, nil)
	return coeffs
}

// mdctForwardOverlapF32Scratch is the scratch-aware version that avoids allocations.
func mdctForwardOverlapF32Scratch(samples []float64, overlap int, coeffs []float64, f []float32, fftIn []complex64, fftOut []complex64, fftTmp []kissCpx) {
	if len(samples) == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap > len(samples) {
		overlap = len(samples)
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 {
		return
	}

	n2 := frameSize
	n := n2 * 2
	n4 := n2 / 2
	if n4 <= 0 {
		return
	}

	trig := getMDCTTrigF32(n)
	var window []float32
	if overlap > 0 {
		window = GetWindowBufferF32(overlap)
	}

	// Use provided buffers or allocate
	if f == nil || len(f) < n2 {
		f = make([]float32, n2)
	}
	if fftIn == nil || len(fftIn) < n4 {
		fftIn = make([]complex64, n4)
	}
	if fftOut == nil || len(fftOut) < n4 {
		fftOut = make([]complex64, n4)
	}
	if fftTmp == nil || len(fftTmp) < n4 {
		fftTmp = make([]kissCpx, n4)
	}
	if coeffs == nil || len(coeffs) < n2 {
		coeffs = make([]float64, n2)
	}

	xp1 := overlap / 2
	xp2 := n2 - 1 + overlap/2
	wp1 := overlap / 2
	wp2 := overlap/2 - 1
	i := 0
	limit1 := (overlap + 3) >> 2

	for ; i < limit1; i++ {
		f[2*i] = float32(samples[xp1+n2])*window[wp2] + float32(samples[xp2])*window[wp1]
		f[2*i+1] = float32(samples[xp1])*window[wp1] - float32(samples[xp2-n2])*window[wp2]
		xp1 += 2
		xp2 -= 2
		wp1 += 2
		wp2 -= 2
	}

	wp1 = 0
	wp2 = overlap - 1
	for ; i < n4-limit1; i++ {
		f[2*i] = float32(samples[xp2])
		f[2*i+1] = float32(samples[xp1])
		xp1 += 2
		xp2 -= 2
	}

	for ; i < n4; i++ {
		f[2*i] = -float32(samples[xp1-n2])*window[wp1] + float32(samples[xp2])*window[wp2]
		f[2*i+1] = float32(samples[xp1])*window[wp2] + float32(samples[xp2+n2])*window[wp1]
		xp1 += 2
		xp2 -= 2
		wp1 += 2
		wp2 -= 2
	}

	scale := float32(1.0 / float64(n4))
	for i = 0; i < n4; i++ {
		re := f[2*i]
		im := f[2*i+1]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 - im*t1
		yi := im*t0 + re*t1
		fftIn[i] = complex(yr*scale, yi*scale)
	}

	kissFFT32To(fftOut, fftIn[:n4], fftTmp)
	for i = 0; i < n4; i++ {
		re := real(fftOut[i])
		im := imag(fftOut[i])
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := im*t1 - re*t0
		yi := re*t1 + im*t0
		coeffs[2*i] = float64(yr)
		coeffs[n2-1-2*i] = float64(yi)
	}
}

// mdctScratch computes the MDCT using scratch buffers to avoid allocations.
func mdctScratch(samples []float64, scratch *encoderScratch) []float64 {
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) {
			return mdctForwardOverlapScratch(samples, Overlap, scratch)
		}
	}

	return mdctStandard(samples)
}

// mdctScratchInto computes the MDCT into a provided output buffer using scratch buffers.
func mdctScratchInto(samples []float64, coeffs []float64, scratch *encoderScratch) []float64 {
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) && len(coeffs) >= frameSize {
			mdctForwardOverlapF32Scratch(samples, Overlap, coeffs,
				scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
			return coeffs[:frameSize]
		}
	}

	return mdctStandard(samples)
}

// mdctShortScratch computes the short-block MDCT using scratch buffers.
func mdctShortScratch(samples []float64, shortBlocks int, scratch *encoderScratch) []float64 {
	if shortBlocks <= 1 {
		return mdctScratch(samples, scratch)
	}
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) && frameSize%shortBlocks == 0 {
			return mdctForwardShortOverlapScratch(samples, Overlap, shortBlocks, scratch)
		}
	}

	return mdctShortStandard(samples, shortBlocks)
}

// mdctShortScratchInto computes short-block MDCT into a provided output buffer.
func mdctShortScratchInto(samples []float64, shortBlocks int, output []float64, scratch *encoderScratch) []float64 {
	if shortBlocks <= 1 {
		return mdctScratchInto(samples, output, scratch)
	}
	if len(samples) == 0 {
		return nil
	}

	if len(samples) > Overlap {
		frameSize := len(samples) - Overlap
		if ValidFrameSize(frameSize) && frameSize%shortBlocks == 0 && len(output) >= frameSize {
			return mdctForwardShortOverlapScratchInto(samples, Overlap, shortBlocks, output, scratch)
		}
	}

	return mdctShortStandard(samples, shortBlocks)
}

// mdctShortBlocksCore is a helper that processes multiple short MDCT blocks.
// It calls blockMDCT for each short block and interleaves results into output.
func mdctShortBlocksCore(samples []float64, overlap, shortBlocks, shortSize int, output, blockCoeffs []float64, blockMDCT func(block, coeffs []float64)) {
	for b := 0; b < shortBlocks; b++ {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}

		// Compute short block MDCT
		blockMDCT(samples[start:end], blockCoeffs)

		// Interleave coefficients into output
		for i, v := range blockCoeffs {
			outIdx := b + i*shortBlocks
			if outIdx < len(output) {
				output[outIdx] = v
			}
		}
	}
}

// mdctForwardShortOverlapScratchInto computes short-block MDCT into a provided output buffer.
func mdctForwardShortOverlapScratchInto(samples []float64, overlap, shortBlocks int, output []float64, scratch *encoderScratch) []float64 {
	if shortBlocks <= 1 {
		if len(output) >= len(samples)-overlap {
			mdctForwardOverlapF32Scratch(samples, overlap, output,
				scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
			return output[:len(samples)-overlap]
		}
		return mdctForwardOverlapScratch(samples, overlap, scratch)
	}
	if len(samples) <= overlap || overlap < 0 {
		return nil
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 || frameSize%shortBlocks != 0 {
		return nil
	}

	shortSize := frameSize / shortBlocks

	// Use scratch buffer for per-block coefficients
	blockCoeffs := ensureFloat64Slice(&scratch.mdctBlockCoeffs, shortSize)

	mdctShortBlocksCore(samples, overlap, shortBlocks, shortSize, output, blockCoeffs,
		func(block []float64, coeffs []float64) {
			mdctForwardOverlapF32Scratch(block, overlap, coeffs,
				scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)
		})

	return output[:frameSize]
}

// mdctForwardOverlapScratch computes the MDCT forward transform using scratch buffers.
func mdctForwardOverlapScratch(samples []float64, overlap int, scratch *encoderScratch) []float64 {
	frameSize := len(samples) - overlap
	if frameSize <= 0 {
		return nil
	}

	// Use scratch buffer for coeffs output
	coeffs := ensureFloat64Slice(&scratch.mdctCoeffs, frameSize)

	// Call the scratch-aware version with all buffers
	mdctForwardOverlapF32Scratch(samples, overlap, coeffs,
		scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)

	return coeffs
}

// mdctForwardShortOverlapScratch computes short-block MDCT using scratch buffers.
func mdctForwardShortOverlapScratch(samples []float64, overlap, shortBlocks int, scratch *encoderScratch) []float64 {
	if shortBlocks <= 1 {
		return mdctForwardOverlapScratch(samples, overlap, scratch)
	}
	if len(samples) <= overlap || overlap < 0 {
		return nil
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 || frameSize%shortBlocks != 0 {
		return nil
	}

	shortSize := frameSize / shortBlocks
	output := ensureFloat64Slice(&scratch.mdctCoeffs, frameSize)

	// Use scratch buffer for per-block coefficients
	blockCoeffs := ensureFloat64Slice(&scratch.mdctBlockCoeffs, shortSize)

	for b := 0; b < shortBlocks; b++ {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}

		// Compute short block MDCT using scratch buffers
		mdctForwardOverlapF32Scratch(samples[start:end], overlap, blockCoeffs,
			scratch.mdctF, scratch.mdctFFTIn, scratch.mdctFFTOut, scratch.mdctFFTTmp)

		for i, v := range blockCoeffs {
			outIdx := b + i*shortBlocks
			if outIdx < len(output) {
				output[outIdx] = v
			}
		}
	}

	return output
}

// mdctForwardShortOverlap computes interleaved MDCT coefficients for transient frames.
// samples length must be frameSize+overlap.
func mdctForwardShortOverlap(samples []float64, overlap, shortBlocks int) []float64 {
	if shortBlocks <= 1 {
		return mdctForwardOverlap(samples, overlap)
	}
	if len(samples) <= overlap || overlap < 0 {
		return nil
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 || frameSize%shortBlocks != 0 {
		return nil
	}

	shortSize := frameSize / shortBlocks
	output := make([]float64, frameSize)

	for b := 0; b < shortBlocks; b++ {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}
		blockCoeffs := mdctForwardOverlap(samples[start:end], overlap)
		for i, v := range blockCoeffs {
			outIdx := b + i*shortBlocks
			if outIdx < len(output) {
				output[outIdx] = v
			}
		}
	}

	return output
}

// mdctStandard computes the direct MDCT for legacy 2*N inputs.
func mdctStandard(samples []float64) []float64 {
	if len(samples) == 0 {
		return nil
	}

	// Input is 2*N samples, output is N coefficients
	N2 := len(samples)
	N := N2 / 2
	if N <= 0 {
		return nil
	}

	windowed := make([]float64, N2)
	copy(windowed, samples)
	applyMDCTWindow(windowed)

	coeffs := make([]float64, N)
	// scale = 1.0 for mdctStandard (no normalization)
	mdctCoreCompute(windowed, coeffs, 1.0)

	return coeffs
}

func mdctShortStandard(samples []float64, shortBlocks int) []float64 {
	totalSamples := len(samples)
	if totalSamples == 0 {
		return nil
	}

	shortSampleSize := totalSamples / shortBlocks
	shortCoeffSize := shortSampleSize / 2
	if shortSampleSize <= 0 || shortCoeffSize <= 0 {
		return mdctStandard(samples)
	}

	totalCoeffs := shortCoeffSize * shortBlocks
	output := make([]float64, totalCoeffs)

	for b := 0; b < shortBlocks; b++ {
		shortSamples := make([]float64, shortSampleSize)
		startIdx := b * shortSampleSize
		for i := 0; i < shortSampleSize && startIdx+i < totalSamples; i++ {
			shortSamples[i] = samples[startIdx+i]
		}

		shortCoeffs := mdctDirect(shortSamples)
		for i := 0; i < len(shortCoeffs) && i < shortCoeffSize; i++ {
			outIdx := b + i*shortBlocks
			if outIdx < totalCoeffs {
				output[outIdx] = shortCoeffs[i]
			}
		}
	}

	return output
}
