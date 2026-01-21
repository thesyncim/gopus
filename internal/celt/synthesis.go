package celt

// Overlap-add synthesis for CELT frame reconstruction.
// This file implements the final stage of CELT decoding: converting
// frequency-domain coefficients to time-domain audio samples with
// proper windowing and overlap-add for seamless frame concatenation.
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/celt_decoder.c

// OverlapAdd combines the current frame with the previous overlap.
// This is the core operation for continuous audio reconstruction in CELT.
//
// Parameters:
//   - current: windowed IMDCT output for current frame
//   - prevOverlap: tail samples from previous frame (overlap region)
//   - overlap: number of overlap samples (typically 120 for CELT)
//
// Returns:
//   - output: reconstructed samples (len = len(current) - overlap)
//   - newOverlap: tail to save for next frame's overlap-add
//
// The overlap-add operation:
// 1. First 'overlap' samples: sum current + prevOverlap
// 2. Middle samples: copy directly from current
// 3. Save last 'overlap' samples for next frame
func OverlapAdd(current, prevOverlap []float64, overlap int) (output, newOverlap []float64) {
	n := len(current)
	if n <= overlap {
		// Edge case: frame too short
		if n == 0 {
			return nil, prevOverlap
		}
		output = make([]float64, n)
		for i := 0; i < n && i < len(prevOverlap); i++ {
			output[i] = prevOverlap[i] + current[i]
		}
		newOverlap = make([]float64, overlap)
		return output, newOverlap
	}

	// Normal case: frame longer than overlap
	outputLen := n - overlap
	output = make([]float64, outputLen)

	// First 'overlap' samples: sum with previous frame's tail
	for i := 0; i < overlap; i++ {
		prev := 0.0
		if i < len(prevOverlap) {
			prev = prevOverlap[i]
		}
		output[i] = prev + current[i]
	}

	// Middle samples (overlap to n-overlap): copy directly
	copy(output[overlap:], current[overlap:n-overlap])

	// Save new overlap: last 'overlap' samples of current
	newOverlap = make([]float64, overlap)
	copy(newOverlap, current[n-overlap:])

	return output, newOverlap
}

// OverlapAddInPlace performs overlap-add modifying prevOverlap in place.
// This variant avoids allocation for the overlap buffer.
//
// Returns: output samples only (prevOverlap is modified to contain new overlap)
func OverlapAddInPlace(current []float64, prevOverlap []float64, overlap int) []float64 {
	n := len(current)
	if n <= overlap || len(prevOverlap) < overlap {
		return current
	}

	outputLen := n - overlap
	output := make([]float64, outputLen)

	// First 'overlap' samples: sum with previous
	for i := 0; i < overlap; i++ {
		output[i] = prevOverlap[i] + current[i]
	}

	// Middle samples
	copy(output[overlap:], current[overlap:n-overlap])

	// Update prevOverlap with new tail
	copy(prevOverlap, current[n-overlap:])

	return output
}

// Synthesize performs full IMDCT + windowing + overlap-add for decoded coefficients.
// This is the main synthesis function called by the decoder.
//
// Parameters:
//   - coeffs: MDCT coefficients from DecodeBands
//   - transient: true if frame uses short blocks (for transients)
//   - shortBlocks: number of short MDCTs if transient (1, 2, 4, or 8)
//
// Returns: PCM samples for this frame
func (d *Decoder) Synthesize(coeffs []float64, transient bool, shortBlocks int) []float64 {
	if len(coeffs) == 0 {
		return nil
	}

	var imdctOut []float64

	if transient && shortBlocks > 1 {
		// Transient mode: multiple short IMDCTs
		imdctOut = IMDCTShort(coeffs, shortBlocks)
	} else {
		// Normal mode: single long IMDCT
		imdctOut = IMDCT(coeffs)
	}

	if len(imdctOut) == 0 {
		return nil
	}

	// Apply Vorbis window
	ApplyWindow(imdctOut, Overlap)

	// Perform overlap-add with previous frame's tail
	output, newOverlap := OverlapAdd(imdctOut, d.overlapBuffer, Overlap)

	// Save overlap for next frame
	d.SetOverlapBuffer(newOverlap)

	return output
}

// SynthesizeStereo performs synthesis for stereo frames.
// Handles both channels with proper interleaving.
//
// Parameters:
//   - coeffsL, coeffsR: MDCT coefficients for left and right channels
//   - transient: true if using short blocks
//   - shortBlocks: number of short MDCTs
//
// Returns: interleaved stereo samples [L0, R0, L1, R1, ...]
func (d *Decoder) SynthesizeStereo(coeffsL, coeffsR []float64, transient bool, shortBlocks int) []float64 {
	if len(coeffsL) == 0 && len(coeffsR) == 0 {
		return nil
	}

	// IMDCT for both channels
	var imdctL, imdctR []float64

	if transient && shortBlocks > 1 {
		imdctL = IMDCTShort(coeffsL, shortBlocks)
		imdctR = IMDCTShort(coeffsR, shortBlocks)
	} else {
		imdctL = IMDCT(coeffsL)
		imdctR = IMDCT(coeffsR)
	}

	// Apply window to both channels
	ApplyWindow(imdctL, Overlap)
	ApplyWindow(imdctR, Overlap)

	// Split overlap buffer: first half is left, second half is right
	overlapL := d.overlapBuffer[:Overlap]
	overlapR := d.overlapBuffer[Overlap:]

	// Overlap-add for both channels
	outputL, newOverlapL := OverlapAdd(imdctL, overlapL, Overlap)
	outputR, newOverlapR := OverlapAdd(imdctR, overlapR, Overlap)

	// Update overlap buffer
	copy(d.overlapBuffer[:Overlap], newOverlapL)
	copy(d.overlapBuffer[Overlap:], newOverlapR)

	// Interleave stereo output
	n := len(outputL)
	if len(outputR) < n {
		n = len(outputR)
	}

	stereo := make([]float64, n*2)
	for i := 0; i < n; i++ {
		stereo[2*i] = outputL[i]
		stereo[2*i+1] = outputR[i]
	}

	return stereo
}

// WindowAndOverlap applies Vorbis window and performs overlap-add.
// This is a combined operation for efficiency.
//
// Parameters:
//   - imdctOut: raw IMDCT output (will be windowed in place)
//
// Returns: reconstructed samples after overlap-add
func (d *Decoder) WindowAndOverlap(imdctOut []float64) []float64 {
	if len(imdctOut) == 0 {
		return nil
	}

	// Apply window
	ApplyWindow(imdctOut, Overlap)

	// Overlap-add
	output, newOverlap := OverlapAdd(imdctOut, d.overlapBuffer, Overlap)

	// Save overlap for next frame
	d.SetOverlapBuffer(newOverlap)

	return output
}

// SynthesizeWithConfig performs synthesis with explicit configuration.
// Useful for testing or non-standard configurations.
func SynthesizeWithConfig(coeffs []float64, overlap int, transient bool, shortBlocks int, prevOverlap []float64) (output, newOverlap []float64) {
	if len(coeffs) == 0 {
		return nil, prevOverlap
	}

	var imdctOut []float64

	if transient && shortBlocks > 1 {
		imdctOut = IMDCTShort(coeffs, shortBlocks)
	} else {
		imdctOut = IMDCT(coeffs)
	}

	if len(imdctOut) == 0 {
		return nil, prevOverlap
	}

	// Apply window
	ApplyWindow(imdctOut, overlap)

	// Overlap-add
	return OverlapAdd(imdctOut, prevOverlap, overlap)
}
