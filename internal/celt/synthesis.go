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

// OverlapAddShortOverlap combines overlap for CELT short-overlap IMDCT output.
// current length is frameSize + overlap, output length is frameSize.
func OverlapAddShortOverlap(current, prevOverlap []float64, frameSize, overlap int) (output, newOverlap []float64) {
	if frameSize <= 0 || overlap < 0 {
		return nil, prevOverlap
	}
	if len(current) < frameSize+overlap {
		return nil, prevOverlap
	}

	output = make([]float64, frameSize)

	for i := 0; i < overlap && i < len(prevOverlap); i++ {
		output[i] = prevOverlap[i] + current[i]
	}
	for i := len(prevOverlap); i < overlap; i++ {
		output[i] = current[i]
	}

	copy(output[overlap:], current[overlap:frameSize])

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

func synthesizeChannelWithOverlap(coeffs []float64, prevOverlap []float64, overlap int, transient bool, shortBlocks int) (output, newOverlap []float64) {
	frameSize := len(coeffs)
	if frameSize == 0 {
		return nil, prevOverlap
	}
	if overlap < 0 {
		return nil, prevOverlap
	}

	prev := prevOverlap
	if len(prev) < overlap {
		tmp := make([]float64, overlap)
		copy(tmp, prev)
		prev = tmp
	} else if len(prev) > overlap {
		prev = prev[:overlap]
	}

	if transient && shortBlocks > 1 {
		shortSize := frameSize / shortBlocks
		if shortSize <= 0 {
			return nil, prevOverlap
		}

		out := make([]float64, frameSize+overlap)
		if overlap > 0 && len(prev) >= overlap {
			copy(out[:overlap], prev[:overlap])
		}

		// Process each short block and copy into the shared buffer.
		for b := 0; b < shortBlocks; b++ {
			// Extract interleaved coefficients for this short block
			shortCoeffs := make([]float64, shortSize)
			for i := 0; i < shortSize; i++ {
				idx := b + i*shortBlocks
				if idx < frameSize {
					shortCoeffs[i] = coeffs[idx]
				}
			}

			blockStart := b * shortSize
			blockPrev := out[blockStart : blockStart+overlap]
			blockOut := imdctOverlapWithPrev(shortCoeffs, blockPrev, overlap)
			copy(out[blockStart:], blockOut)
		}

		output = out[:frameSize]
		newOverlap = make([]float64, overlap)
		copy(newOverlap, out[frameSize:frameSize+overlap])
		return output, newOverlap
	}

	imdctOut := IMDCTOverlapWithPrev(coeffs, prev, overlap)
	if len(imdctOut) < frameSize {
		return nil, prevOverlap
	}
	output = imdctOut[:frameSize]
	newOverlap = make([]float64, overlap)
	if overlap > 0 && frameSize+overlap <= len(imdctOut) {
		copy(newOverlap, imdctOut[frameSize:frameSize+overlap])
	}
	return output, newOverlap
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
	output, newOverlap := synthesizeChannelWithOverlap(coeffs, d.overlapBuffer, Overlap, transient, shortBlocks)
	if len(output) == 0 {
		return nil
	}
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
	if len(d.overlapBuffer) < Overlap*2 {
		d.overlapBuffer = make([]float64, Overlap*2)
	}
	overlapL := d.overlapBuffer[:Overlap]
	overlapR := d.overlapBuffer[Overlap : Overlap*2]

	outputL, newOverlapL := synthesizeChannelWithOverlap(coeffsL, overlapL, Overlap, transient, shortBlocks)
	outputR, newOverlapR := synthesizeChannelWithOverlap(coeffsR, overlapR, Overlap, transient, shortBlocks)

	if len(newOverlapL) >= Overlap {
		copy(d.overlapBuffer[:Overlap], newOverlapL[:Overlap])
	}
	if len(newOverlapR) >= Overlap {
		copy(d.overlapBuffer[Overlap:Overlap*2], newOverlapR[:Overlap])
	}

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
	output, newOverlap := synthesizeChannelWithOverlap(coeffs, d.overlapBuffer, Overlap, true, shortBlocks)
	if len(output) == 0 {
		return nil
	}
	d.SetOverlapBuffer(newOverlap)
	return output
}

func (d *Decoder) synthesizeShortStereo(coeffsL, coeffsR []float64, shortBlocks int) []float64 {
	if len(d.overlapBuffer) < Overlap*2 {
		d.overlapBuffer = make([]float64, Overlap*2)
	}
	overlapL := d.overlapBuffer[:Overlap]
	overlapR := d.overlapBuffer[Overlap : Overlap*2]
	outputL, newOverlapL := synthesizeChannelWithOverlap(coeffsL, overlapL, Overlap, true, shortBlocks)
	outputR, newOverlapR := synthesizeChannelWithOverlap(coeffsR, overlapR, Overlap, true, shortBlocks)

	if len(newOverlapL) >= Overlap {
		copy(d.overlapBuffer[:Overlap], newOverlapL[:Overlap])
	}
	if len(newOverlapR) >= Overlap {
		copy(d.overlapBuffer[Overlap:Overlap*2], newOverlapR[:Overlap])
	}

	n := len(outputL)
	if len(outputR) < n {
		n = len(outputR)
	}
	output := make([]float64, n*2)
	for i := 0; i < n; i++ {
		output[2*i] = outputL[i]
		output[2*i+1] = outputR[i]
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

	frameSize := len(imdctOut) - Overlap
	if frameSize <= 0 {
		return nil
	}

	output := imdctOut[:frameSize]
	if frameSize+Overlap <= len(imdctOut) {
		d.SetOverlapBuffer(imdctOut[frameSize : frameSize+Overlap])
	}

	return output
}

// SynthesizeWithConfig performs synthesis with explicit configuration.
// Useful for testing or non-standard configurations.
func SynthesizeWithConfig(coeffs []float64, overlap int, transient bool, shortBlocks int, prevOverlap []float64) (output, newOverlap []float64) {
	if len(coeffs) == 0 {
		return nil, prevOverlap
	}
	output, newOverlap = synthesizeChannelWithOverlap(coeffs, prevOverlap, overlap, transient, shortBlocks)
	return output, newOverlap
}
