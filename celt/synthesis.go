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
func OverlapAdd(current, prevOverlap []float32, overlap int) (output, newOverlap []float32) {
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
		output = make([]float32, frameSize)
		for i := 0; i < frameSize && i < len(prevOverlap); i++ {
			output[i] = prevOverlap[i] + current[i]
		}
		newOverlap = make([]float32, overlap)
		return output, newOverlap
	}

	// Output is frameSize = n/2 samples
	frameSize := n / 2
	output = make([]float32, frameSize)

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
	newOverlap = make([]float32, overlap)
	copy(newOverlap, current[frameSize:frameSize+overlap])

	return output, newOverlap
}

// OverlapAddShortOverlap combines overlap for CELT short-overlap IMDCT output.
// current length is frameSize + overlap, output length is frameSize.
func OverlapAddShortOverlap(current, prevOverlap []float32, frameSize, overlap int) (output, newOverlap []float32) {
	if frameSize <= 0 || overlap < 0 {
		return nil, prevOverlap
	}
	if len(current) < frameSize+overlap {
		return nil, prevOverlap
	}

	output = make([]float32, frameSize)

	for i := 0; i < overlap && i < len(prevOverlap); i++ {
		output[i] = prevOverlap[i] + current[i]
	}
	for i := len(prevOverlap); i < overlap; i++ {
		output[i] = current[i]
	}

	copy(output[overlap:], current[overlap:frameSize])

	newOverlap = make([]float32, overlap)
	copy(newOverlap, current[frameSize:frameSize+overlap])

	return output, newOverlap
}

// OverlapAddInPlace performs overlap-add modifying prevOverlap in place.
// This variant avoids allocation for the overlap buffer.
//
// Returns: output samples only (prevOverlap is modified to contain new overlap)
func OverlapAddInPlace(current []float32, prevOverlap []float32, overlap int) []float32 {
	n := len(current) // 2*frameSize from IMDCT
	if n < 2*overlap || len(prevOverlap) < overlap {
		return current
	}

	// Output is frameSize = n/2 samples
	frameSize := n / 2
	output := make([]float32, frameSize)

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

func synthesizeChannelWithOverlapScratchF32(coeffs []float32, prevOverlap []celtSig, overlap int, transient bool, shortBlocks int, out []float32, scratchF32 *imdctScratchF32, shortCoeffs []float32) (output []float32) {
	frameSize := len(coeffs)
	if frameSize == 0 {
		return nil
	}
	if overlap < 0 || len(prevOverlap) < overlap {
		return nil
	}
	if len(prevOverlap) > overlap {
		prevOverlap = prevOverlap[:overlap]
	}

	needed := frameSize + overlap
	if len(out) < needed {
		return nil
	}

	if transient && shortBlocks > 1 {
		clear(out[:needed])
		if overlap > 0 {
			for i := 0; i < overlap; i++ {
				out[i] = float32(prevOverlap[i])
			}
		}

		shortSize := frameSize / shortBlocks
		if shortSize <= 0 || len(shortCoeffs) < shortSize {
			return nil
		}

		if shortSize*shortBlocks == frameSize {
			for b := 0; b < shortBlocks; b++ {
				idx := b
				for i := 0; i < shortSize; i++ {
					shortCoeffs[i] = coeffs[idx]
					idx += shortBlocks
				}
				imdctInPlaceScratchF32Spectrum(shortCoeffs[:shortSize], out, b*shortSize, overlap, scratchF32)
			}
		} else {
			for b := 0; b < shortBlocks; b++ {
				for i := 0; i < shortSize; i++ {
					idx := b + i*shortBlocks
					if idx < frameSize {
						shortCoeffs[i] = coeffs[idx]
					} else {
						shortCoeffs[i] = 0
					}
				}
				imdctInPlaceScratchF32Spectrum(shortCoeffs[:shortSize], out, b*shortSize, overlap, scratchF32)
			}
		}

		return out[:needed]
	}

	output = imdctOverlapWithPrevScratchF32Output32(coeffs, prevOverlap, overlap, scratchF32)
	if len(output) < needed {
		return nil
	}
	copy(out[:needed], output[:needed])
	return out[:needed]
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
func (d *Decoder) Synthesize(coeffs []float32, transient bool, shortBlocks int) []float32 {
	if len(coeffs) == 0 {
		return nil
	}
	out := ensureFloat32Slice(&d.scratchSynthF32, len(coeffs)+Overlap)
	shortCoeffs := ensureFloat32Slice(&d.scratchShortCoeffsF32, len(coeffs))
	output := synthesizeChannelWithOverlapScratchF32(coeffs, d.overlapBuffer, Overlap, transient, shortBlocks, out, &d.scratchIMDCTF32, shortCoeffs)
	if len(output) == 0 {
		return nil
	}
	if Overlap > 0 && len(output) >= len(coeffs)+Overlap {
		copy(d.overlapBuffer[:Overlap], output[len(coeffs):len(coeffs)+Overlap])
	}
	return output[:len(coeffs)]
}

func (d *Decoder) SynthesizeFloat32(coeffs []float32, transient bool, shortBlocks int) []float32 {
	if len(coeffs) == 0 {
		return nil
	}
	out := ensureFloat32Slice(&d.scratchSynthF32, len(coeffs)+Overlap)
	shortCoeffs := ensureFloat32Slice(&d.scratchShortCoeffsF32, len(coeffs))
	output := synthesizeChannelWithOverlapScratchF32(coeffs, d.overlapBuffer, Overlap, transient, shortBlocks, out, &d.scratchIMDCTF32, shortCoeffs)
	if len(output) == 0 {
		return nil
	}
	if Overlap > 0 && len(output) >= len(coeffs)+Overlap {
		copy(d.overlapBuffer[:Overlap], output[len(coeffs):len(coeffs)+Overlap])
	}
	return output[:len(coeffs)]
}

func (d *Decoder) synthesizeMonoLongToFloat32(coeffs []float32) []float32 {
	if len(coeffs) == 0 {
		return nil
	}
	if len(d.overlapBuffer) < Overlap {
		buf := make([]celtSig, Overlap)
		copy(buf, d.overlapBuffer)
		d.overlapBuffer = buf
	}

	outF32 := imdctOverlapWithPrevScratchF32Output32(coeffs, d.overlapBuffer[:Overlap], Overlap, &d.scratchIMDCTF32)
	if len(outF32) < len(coeffs)+Overlap {
		return nil
	}
	if Overlap > 0 {
		copy(d.overlapBuffer[:Overlap], outF32[len(coeffs):len(coeffs)+Overlap])
	}
	return outF32[:len(coeffs)]
}

func (d *Decoder) synthesizeStereoPlanarLongToFloat32(coeffsL, coeffsR []float32) (outL, outR []float32) {
	if len(coeffsL) == 0 || len(coeffsR) == 0 {
		return nil, nil
	}
	if len(d.overlapBuffer) < Overlap*2 {
		d.overlapBuffer = make([]celtSig, Overlap*2)
	}
	overlapL := d.overlapBuffer[:Overlap]
	overlapR := d.overlapBuffer[Overlap : Overlap*2]

	outLFull := imdctOverlapWithPrevScratchF32Output32(coeffsL, overlapL, Overlap, &d.scratchIMDCTF32)
	outRFull := imdctOverlapWithPrevScratchF32Output32(coeffsR, overlapR, Overlap, &d.scratchIMDCTF32R)
	if len(outLFull) < len(coeffsL)+Overlap || len(outRFull) < len(coeffsR)+Overlap {
		return nil, nil
	}
	if Overlap > 0 {
		copy(overlapL, outLFull[len(coeffsL):len(coeffsL)+Overlap])
		copy(overlapR, outRFull[len(coeffsR):len(coeffsR)+Overlap])
	}
	return outLFull[:len(coeffsL)], outRFull[:len(coeffsR)]
}

func (d *Decoder) synthesizeStereoPlanar(coeffsL, coeffsR []float32, transient bool, shortBlocks int) (outL, outR []float32) {
	if len(coeffsL) == 0 && len(coeffsR) == 0 {
		return nil, nil
	}
	if len(d.overlapBuffer) < Overlap*2 {
		d.overlapBuffer = make([]celtSig, Overlap*2)
	}
	overlapL := d.overlapBuffer[:Overlap]
	overlapR := d.overlapBuffer[Overlap : Overlap*2]

	bufL := ensureFloat32Slice(&d.scratchSynthF32, len(coeffsL)+Overlap)
	bufR := ensureFloat32Slice(&d.scratchSynthRF32, len(coeffsR)+Overlap)
	shortCoeffs := ensureFloat32Slice(&d.scratchShortCoeffsF32, max(len(coeffsL), len(coeffsR)))
	outLFull := synthesizeChannelWithOverlapScratchF32(coeffsL, overlapL, Overlap, transient, shortBlocks, bufL, &d.scratchIMDCTF32, shortCoeffs)
	outRFull := synthesizeChannelWithOverlapScratchF32(coeffsR, overlapR, Overlap, transient, shortBlocks, bufR, &d.scratchIMDCTF32, shortCoeffs)
	if len(outLFull) == 0 || len(outRFull) == 0 {
		return nil, nil
	}

	if Overlap > 0 && len(outLFull) >= len(coeffsL)+Overlap {
		copy(overlapL, outLFull[len(coeffsL):len(coeffsL)+Overlap])
	}
	if Overlap > 0 && len(outRFull) >= len(coeffsR)+Overlap {
		copy(overlapR, outRFull[len(coeffsR):len(coeffsR)+Overlap])
	}

	return outLFull[:len(coeffsL)], outRFull[:len(coeffsR)]
}

func (d *Decoder) synthesizeStereoPlanarFloat32(coeffsL, coeffsR []float32, transient bool, shortBlocks int) (outL, outR []float32) {
	if len(coeffsL) == 0 && len(coeffsR) == 0 {
		return nil, nil
	}
	if len(d.overlapBuffer) < Overlap*2 {
		d.overlapBuffer = make([]celtSig, Overlap*2)
	}
	overlapL := d.overlapBuffer[:Overlap]
	overlapR := d.overlapBuffer[Overlap : Overlap*2]

	bufL := ensureFloat32Slice(&d.scratchSynthF32, len(coeffsL)+Overlap)
	bufR := ensureFloat32Slice(&d.scratchSynthRF32, len(coeffsR)+Overlap)
	shortCoeffs := ensureFloat32Slice(&d.scratchShortCoeffsF32, max(len(coeffsL), len(coeffsR)))
	outLFull := synthesizeChannelWithOverlapScratchF32(coeffsL, overlapL, Overlap, transient, shortBlocks, bufL, &d.scratchIMDCTF32, shortCoeffs)
	outRFull := synthesizeChannelWithOverlapScratchF32(coeffsR, overlapR, Overlap, transient, shortBlocks, bufR, &d.scratchIMDCTF32, shortCoeffs)
	if len(outLFull) == 0 || len(outRFull) == 0 {
		return nil, nil
	}

	if Overlap > 0 && len(outLFull) >= len(coeffsL)+Overlap {
		copy(overlapL, outLFull[len(coeffsL):len(coeffsL)+Overlap])
	}
	if Overlap > 0 && len(outRFull) >= len(coeffsR)+Overlap {
		copy(overlapR, outRFull[len(coeffsR):len(coeffsR)+Overlap])
	}

	return outLFull[:len(coeffsL)], outRFull[:len(coeffsR)]
}

func (d *Decoder) synthesizeStereoPlanarFromMonoLong(coeffs []float32) (outL, outR []float32) {
	if len(coeffs) == 0 {
		return nil, nil
	}
	if len(d.overlapBuffer) < Overlap*2 {
		d.overlapBuffer = make([]celtSig, Overlap*2)
	}
	overlapL := d.overlapBuffer[:Overlap]
	overlapR := d.overlapBuffer[Overlap : Overlap*2]

	outLFull := imdctOverlapWithPrevScratchF32Output32(coeffs, overlapL, Overlap, &d.scratchIMDCTF32)
	outRFull := imdctOverlapWithPrevScratchF32Output32(coeffs, overlapR, Overlap, &d.scratchIMDCTF32R)
	if len(outLFull) < len(coeffs)+Overlap || len(outRFull) < len(coeffs)+Overlap {
		return nil, nil
	}

	if Overlap > 0 {
		copy(overlapL, outLFull[len(coeffs):len(coeffs)+Overlap])
		copy(overlapR, outRFull[len(coeffs):len(coeffs)+Overlap])
	}
	return outLFull[:len(coeffs)], outRFull[:len(coeffs)]
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
func (d *Decoder) SynthesizeStereo(coeffsL, coeffsR []float32, transient bool, shortBlocks int) []float32 {
	outputL, outputR := d.synthesizeStereoPlanar(coeffsL, coeffsR, transient, shortBlocks)
	if len(outputL) == 0 || len(outputR) == 0 {
		return nil
	}

	// Interleave stereo output
	n := len(outputL)
	if len(outputR) < n {
		n = len(outputR)
	}

	stereo := ensureFloat32Slice(&d.scratchStereoF32, n*2)
	for i := 0; i < n; i++ {
		stereo[2*i] = outputL[i]
		stereo[2*i+1] = outputR[i]
	}

	return stereo[:n*2]
}

func (d *Decoder) SynthesizeStereoFloat32(coeffsL, coeffsR []float32, transient bool, shortBlocks int) []float32 {
	if len(coeffsL) == 0 || len(coeffsR) == 0 {
		return nil
	}
	outL, outR := d.synthesizeStereoPlanarFloat32(coeffsL, coeffsR, transient, shortBlocks)
	if len(outL) == 0 || len(outR) == 0 {
		return nil
	}
	n := min(len(outL), len(outR))
	stereo := ensureFloat32Slice(&d.scratchStereoF32, n*2)
	for i := 0; i < n; i++ {
		stereo[2*i] = outL[i]
		stereo[2*i+1] = outR[i]
	}
	return stereo[:n*2]
}

// WindowAndOverlap applies Vorbis window and performs overlap-add.
// This is a combined operation for efficiency.
//
// Parameters:
//   - imdctOut: raw IMDCT output (will be windowed in place)
//
// Returns: reconstructed samples after overlap-add
func (d *Decoder) WindowAndOverlap(imdctOut []float32) []float32 {
	if len(imdctOut) == 0 {
		return nil
	}

	frameSize := len(imdctOut) - Overlap
	if frameSize <= 0 {
		return nil
	}

	output := imdctOut[:frameSize]
	if frameSize+Overlap <= len(imdctOut) {
		copyFloat32ToSig(d.overlapBuffer, imdctOut[frameSize:frameSize+Overlap])
	}

	return output
}

// SynthesizeWithConfig performs synthesis with explicit configuration.
// Useful for testing or non-standard configurations.
func SynthesizeWithConfig(coeffs []float32, overlap int, transient bool, shortBlocks int, prevOverlap []float32) (output, newOverlap []float32) {
	if len(coeffs) == 0 {
		return nil, prevOverlap
	}
	prevSig := make([]celtSig, overlap)
	for i := 0; i < overlap && i < len(prevOverlap); i++ {
		prevSig[i] = celtSig(prevOverlap[i])
	}
	out := make([]float32, len(coeffs)+overlap)
	shortCoeffs := make([]float32, len(coeffs))
	var scratch imdctScratchF32
	output = synthesizeChannelWithOverlapScratchF32(coeffs, prevSig, overlap, transient, shortBlocks, out, &scratch, shortCoeffs)
	if len(output) == 0 {
		return nil, prevOverlap
	}
	newOverlap = make([]float32, overlap)
	if overlap > 0 && len(out) >= len(coeffs)+overlap {
		copy(newOverlap, out[len(coeffs):len(coeffs)+overlap])
	}
	return output, newOverlap
}
