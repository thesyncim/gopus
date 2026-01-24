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
//   - current: windowed IMDCT output for current frame (2*frameSize samples)
//   - prevOverlap: tail samples from previous frame (overlap region)
//   - overlap: number of overlap samples (typically 120 for CELT)
//
// Returns:
//   - output: reconstructed samples (frameSize = len(current)/2)
//   - newOverlap: tail to save for next frame's overlap-add
//
// The MDCT/IMDCT overlap-add operation per RFC 6716:
// - IMDCT of N coefficients produces 2N windowed samples
// - Output per frame is N samples (frameSize)
// - First 'overlap' samples: sum current[0:overlap] + prevOverlap
// - Middle samples: copy from current[overlap:frameSize]
// - Save current[frameSize:frameSize+overlap] for next frame
func OverlapAdd(current, prevOverlap []float64, overlap int) (output, newOverlap []float64) {
	n := len(current) // 2*frameSize samples from IMDCT
	if n < 2*overlap {
		// Edge case: frame too short for proper overlap
		if n == 0 {
			return nil, prevOverlap
		}
		// For very short frames, output what we can
		frameSize := n / 2
		if frameSize < 1 {
			frameSize = 1
		}
		output = make([]float64, frameSize)
		for i := 0; i < frameSize && i < len(prevOverlap); i++ {
			output[i] = prevOverlap[i] + current[i]
		}
		newOverlap = make([]float64, overlap)
		return output, newOverlap
	}

	// Output is frameSize = n/2 samples
	frameSize := n / 2
	output = make([]float64, frameSize)

	// First 'overlap' samples: sum with previous frame's saved tail
	for i := 0; i < overlap && i < len(prevOverlap); i++ {
		output[i] = prevOverlap[i] + current[i]
	}
	// If overlap exceeds prevOverlap length, just copy from current
	for i := len(prevOverlap); i < overlap; i++ {
		output[i] = current[i]
	}

	// Middle samples: direct copy from current[overlap : frameSize]
	copy(output[overlap:], current[overlap:frameSize])

	// Save new overlap: current[frameSize : frameSize+overlap]
	newOverlap = make([]float64, overlap)
	copy(newOverlap, current[frameSize:frameSize+overlap])

	return output, newOverlap
}

// OverlapAddInPlace performs overlap-add modifying prevOverlap in place.
// This variant avoids allocation for the overlap buffer.
//
// Returns: output samples only (prevOverlap is modified to contain new overlap)
func OverlapAddInPlace(current []float64, prevOverlap []float64, overlap int) []float64 {
	n := len(current) // 2*frameSize from IMDCT
	if n < 2*overlap || len(prevOverlap) < overlap {
		return current
	}

	// Output is frameSize = n/2 samples
	frameSize := n / 2
	output := make([]float64, frameSize)

	// First 'overlap' samples: sum with previous
	for i := 0; i < overlap; i++ {
		output[i] = prevOverlap[i] + current[i]
	}

	// Middle samples: direct copy from current[overlap : frameSize]
	copy(output[overlap:], current[overlap:frameSize])

	// Update prevOverlap with new tail: current[frameSize : frameSize+overlap]
	copy(prevOverlap, current[frameSize:frameSize+overlap])

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
	if transient && shortBlocks > 1 {
		return d.synthesizeShort(coeffs, shortBlocks)
	}
	// Normal mode: single long IMDCT
	imdctOut := IMDCT(coeffs)

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
	if transient && shortBlocks > 1 {
		return d.synthesizeShortStereo(coeffsL, coeffsR, shortBlocks)
	}

	// IMDCT for both channels
	imdctL := IMDCT(coeffsL)
	imdctR := IMDCT(coeffsR)

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

func (d *Decoder) synthesizeShort(coeffs []float64, shortBlocks int) []float64 {
	frameSize := len(coeffs)
	if frameSize == 0 || shortBlocks <= 1 {
		return nil
	}
	shortSize := frameSize / shortBlocks
	if shortSize <= 0 {
		return nil
	}

	overlap := Overlap
	if overlap > shortSize {
		overlap = shortSize
	}

	output := make([]float64, frameSize)
	prevOverlap := make([]float64, overlap)
	if len(d.overlapBuffer) >= overlap {
		copy(prevOverlap, d.overlapBuffer[:overlap])
	}

	outOffset := 0
	for b := 0; b < shortBlocks; b++ {
		shortCoeffs := make([]float64, shortSize)
		for i := 0; i < shortSize; i++ {
			idx := b + i*shortBlocks
			if idx < frameSize {
				shortCoeffs[i] = coeffs[idx]
			}
		}

		blockOut := IMDCT(shortCoeffs)
		if len(blockOut) == 0 {
			continue
		}
		ApplyWindow(blockOut, overlap)

		blockSamples, newOverlap := OverlapAdd(blockOut, prevOverlap, overlap)
		copy(output[outOffset:], blockSamples)
		outOffset += len(blockSamples)
		prevOverlap = newOverlap
	}

	if len(d.overlapBuffer) >= overlap {
		copy(d.overlapBuffer[:overlap], prevOverlap)
	}

	return output
}

func (d *Decoder) synthesizeShortStereo(coeffsL, coeffsR []float64, shortBlocks int) []float64 {
	frameSize := len(coeffsL)
	if frameSize == 0 || shortBlocks <= 1 {
		return nil
	}
	if len(coeffsR) < frameSize {
		frameSize = len(coeffsR)
	}
	shortSize := frameSize / shortBlocks
	if shortSize <= 0 {
		return nil
	}

	overlap := Overlap
	if overlap > shortSize {
		overlap = shortSize
	}

	output := make([]float64, frameSize*2)
	prevOverlapL := make([]float64, overlap)
	prevOverlapR := make([]float64, overlap)
	if len(d.overlapBuffer) >= Overlap*2 {
		copy(prevOverlapL, d.overlapBuffer[:overlap])
		copy(prevOverlapR, d.overlapBuffer[Overlap:Overlap+overlap])
	}

	for b := 0; b < shortBlocks; b++ {
		shortCoeffsL := make([]float64, shortSize)
		shortCoeffsR := make([]float64, shortSize)
		for i := 0; i < shortSize; i++ {
			idx := b + i*shortBlocks
			if idx < frameSize {
				shortCoeffsL[i] = coeffsL[idx]
				shortCoeffsR[i] = coeffsR[idx]
			}
		}

		blockOutL := IMDCT(shortCoeffsL)
		blockOutR := IMDCT(shortCoeffsR)
		if len(blockOutL) == 0 || len(blockOutR) == 0 {
			continue
		}
		ApplyWindow(blockOutL, overlap)
		ApplyWindow(blockOutR, overlap)

		blockSamplesL, newOverlapL := OverlapAdd(blockOutL, prevOverlapL, overlap)
		blockSamplesR, newOverlapR := OverlapAdd(blockOutR, prevOverlapR, overlap)

		start := b * shortSize
		for i := 0; i < shortSize && start+i < frameSize; i++ {
			output[(start+i)*2] = blockSamplesL[i]
			output[(start+i)*2+1] = blockSamplesR[i]
		}

		prevOverlapL = newOverlapL
		prevOverlapR = newOverlapR
	}

	if len(d.overlapBuffer) >= Overlap*2 {
		copy(d.overlapBuffer[:overlap], prevOverlapL)
		copy(d.overlapBuffer[Overlap:Overlap+overlap], prevOverlapR)
	}

	return output
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
