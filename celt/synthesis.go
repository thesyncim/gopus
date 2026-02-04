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

	out := make([]float64, frameSize+overlap)
	var scratch imdctScratch
	var scratchF32 imdctScratchF32
	shortCoeffs := make([]float64, frameSize)
	output = synthesizeChannelWithOverlapScratch(coeffs, prev, overlap, transient, shortBlocks, out, &scratch, &scratchF32, shortCoeffs)
	if len(output) == 0 {
		return nil, prevOverlap
	}
	newOverlap = make([]float64, overlap)
	if overlap > 0 && frameSize+overlap <= len(out) {
		copy(newOverlap, out[frameSize:frameSize+overlap])
	}
	return output, newOverlap
}

func synthesizeChannelWithOverlapScratch(coeffs []float64, prevOverlap []float64, overlap int, transient bool, shortBlocks int, out []float64, scratch *imdctScratch, scratchF32 *imdctScratchF32, shortCoeffs []float64) (output []float64) {
	frameSize := len(coeffs)
	if frameSize == 0 {
		return nil
	}
	if overlap < 0 {
		return nil
	}

	if len(prevOverlap) < overlap {
		return nil
	}
	if len(prevOverlap) > overlap {
		prevOverlap = prevOverlap[:overlap]
	}

	needed := frameSize + overlap
	if len(out) < needed {
		return nil
	}
	// Clear output for deterministic TDAC windowing.
	for i := 0; i < needed; i++ {
		out[i] = 0
	}
	if overlap > 0 {
		copy(out[:overlap], prevOverlap)
	}

	if transient && shortBlocks > 1 {
		shortSize := frameSize / shortBlocks
		if shortSize <= 0 {
			return nil
		}
		if len(shortCoeffs) < shortSize {
			return nil
		}

		// Process each short block and write into the shared buffer.
		// Use float32 IMDCT to match libopus precision for transient frames.
		for b := 0; b < shortBlocks; b++ {
			// Extract interleaved coefficients for this short block
			for i := 0; i < shortSize; i++ {
				idx := b + i*shortBlocks
				if idx < frameSize {
					shortCoeffs[i] = coeffs[idx]
				} else {
					shortCoeffs[i] = 0
				}
			}

			blockStart := b * shortSize
			imdctInPlaceScratchF32(shortCoeffs[:shortSize], out, blockStart, overlap, scratchF32)
		}

		return out[:frameSize]
	}

	// Use float32 IMDCT to match libopus precision for long blocks too
	imdctOverlapWithPrevScratchF32(out, coeffs, prevOverlap, overlap, scratchF32)
	return out[:frameSize]
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
	out := ensureFloat64Slice(&d.scratchSynth, len(coeffs)+Overlap)
	shortCoeffs := ensureFloat64Slice(&d.scratchShortCoeffs, len(coeffs))
	output := synthesizeChannelWithOverlapScratch(coeffs, d.overlapBuffer, Overlap, transient, shortBlocks, out, &d.scratchIMDCT, &d.scratchIMDCTF32, shortCoeffs)
	if len(output) == 0 {
		return nil
	}
	if Overlap > 0 && len(out) >= len(coeffs)+Overlap {
		copy(d.overlapBuffer, out[len(coeffs):len(coeffs)+Overlap])
	}
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

	outL := ensureFloat64Slice(&d.scratchSynth, len(coeffsL)+Overlap)
	outR := ensureFloat64Slice(&d.scratchSynthR, len(coeffsR)+Overlap)
	shortCoeffs := ensureFloat64Slice(&d.scratchShortCoeffs, max(len(coeffsL), len(coeffsR)))
	outputL := synthesizeChannelWithOverlapScratch(coeffsL, overlapL, Overlap, transient, shortBlocks, outL, &d.scratchIMDCT, &d.scratchIMDCTF32, shortCoeffs)
	outputR := synthesizeChannelWithOverlapScratch(coeffsR, overlapR, Overlap, transient, shortBlocks, outR, &d.scratchIMDCT, &d.scratchIMDCTF32, shortCoeffs)

	if Overlap > 0 && len(outL) >= len(coeffsL)+Overlap {
		copy(d.overlapBuffer[:Overlap], outL[len(coeffsL):len(coeffsL)+Overlap])
	}
	if Overlap > 0 && len(outR) >= len(coeffsR)+Overlap {
		copy(d.overlapBuffer[Overlap:Overlap*2], outR[len(coeffsR):len(coeffsR)+Overlap])
	}

	// Interleave stereo output
	n := len(outputL)
	if len(outputR) < n {
		n = len(outputR)
	}

	stereo := ensureFloat64Slice(&d.scratchStereo, n*2)
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
