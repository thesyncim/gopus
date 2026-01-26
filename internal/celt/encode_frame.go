// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the complete frame encoding pipeline.

package celt

import (
	"errors"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Encoding errors
var (
	// ErrInvalidInputLength indicates the PCM input length doesn't match frame size.
	ErrInvalidInputLength = errors.New("celt: invalid input length")

	// ErrEncodingFailed indicates a general encoding failure.
	ErrEncodingFailed = errors.New("celt: encoding failed")
)

// EncodeFrame encodes a complete CELT frame from PCM samples.
// pcm: input samples (interleaved if stereo), length = frameSize * channels
// frameSize: 120, 240, 480, or 960 samples
// Returns: encoded bytes
//
// The encoding pipeline (mirrors decoder's DecodeFrame):
// 1. Validate inputs
// 2. Get mode configuration
// 3. Detect transient
// 4. Apply pre-emphasis
// 5. Compute MDCT
// 6. Compute band energies
// 7. Normalize bands
// 8. Initialize range encoder
// 9. Encode frame flags (silence, transient, intra)
// 10. For stereo: encode stereo params
// 11. Encode coarse energy
// 12. Compute bit allocation
// 13. Encode fine energy
// 14. Encode bands (PVQ)
// 15. Finalize and return bytes
//
// Reference: RFC 6716 Section 4.3, libopus celt/celt_encoder.c
func (e *Encoder) EncodeFrame(pcm []float64, frameSize int) ([]byte, error) {
	// Step 1: Validate inputs
	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	expectedLen := frameSize * e.channels
	if len(pcm) != expectedLen {
		return nil, ErrInvalidInputLength
	}

	// Step 2: Get mode configuration
	mode := GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Step 3: Detect transient
	transient := e.DetectTransient(pcm, frameSize)
	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Step 4: Apply pre-emphasis with signal scaling
	// Input samples in float range [-1.0, 1.0] are scaled to signal scale (x32768)
	// This matches libopus CELT_SIG_SCALE. The decoder reverses this with scaleSamples(1/32768).
	preemph := e.ApplyPreemphasisWithScaling(pcm)

	// Step 5: Compute MDCT with proper overlap handling
	var mdctCoeffs []float64
	var mdctLeft, mdctRight []float64
	if e.channels == 1 {
		// Mono: MDCT directly with overlap buffer for continuity
		mdctCoeffs = e.computeMDCTWithOverlap(preemph, shortBlocks)
	} else {
		// Stereo: MDCT Left and Right directly
		left, right := DeinterleaveStereo(preemph)

		overlap := Overlap
		if overlap > frameSize {
			overlap = frameSize
		}

		// Ensure overlap buffer is large enough for both channels
		if len(e.overlapBuffer) < 2*overlap {
			newBuf := make([]float64, 2*overlap)
			if len(e.overlapBuffer) > 0 {
				copy(newBuf, e.overlapBuffer)
			}
			e.overlapBuffer = newBuf
		}

		// Split overlap buffer for left and right
		leftHistory := e.overlapBuffer[:overlap]
		rightHistory := e.overlapBuffer[overlap : 2*overlap]

		// Use overlap-aware MDCT for both channels
		mdctLeft = ComputeMDCTWithHistory(left, leftHistory, shortBlocks)
		mdctRight = ComputeMDCTWithHistory(right, rightHistory, shortBlocks)

		// Concatenate: [left coeffs][right coeffs]
		mdctCoeffs = make([]float64, len(mdctLeft)+len(mdctRight))
		copy(mdctCoeffs[:len(mdctLeft)], mdctLeft)
		copy(mdctCoeffs[len(mdctLeft):], mdctRight)
	}

	// Step 6: Compute band energies
	energies := e.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Note: NormalizeBands is called later, AFTER encoding coarse+fine energy,
	// using the quantized energies. This ensures encoder and decoder use the
	// same gain values for normalization/denormalization.

	// Step 7: Initialize range encoder with bitrate-derived size
	// ... (no changes here) ...
	targetBits := e.computeTargetBits(frameSize)
	e.frameBits = targetBits
	defer func() { e.frameBits = 0 }()
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	e.SetRangeEncoder(re)

	// Step 9: Encode frame flags
	// ... (no changes) ...
	isSilence := isFrameSilent(pcm)
	if isSilence {
		re.EncodeBit(1, 15)
		bytes := re.Done()
		return bytes, nil
	}
	re.EncodeBit(0, 15)
	start := 0
	re.EncodeBit(0, 1)
	if lm > 0 {
		var transientBit int
		if transient {
			transientBit = 1
		}
		re.EncodeBit(transientBit, 3)
	}
	intra := e.isIntraFrame()
	var intraBit int
	if intra {
		intraBit = 1
	}
	re.EncodeBit(intraBit, 3)

	// Step 10: Prepare stereo params (will be encoded during allocation)
	// For dual stereo mode: intensity = nbBands (no intensity stereo), dualStereo = true
	intensity := nbBands // No intensity stereo - all bands use dual stereo
	dualStereo := false
	if e.channels == 2 {
		dualStereo = true // Use dual stereo mode (encode L and R independently)
	}

	// Step 11: Encode coarse energy
	prev1LogE := append([]float64(nil), e.prevEnergy...)
	quantizedEnergies := e.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	// Step 11.1: Encode TF (time-frequency) resolution
	end := nbBands
	tfEncode(re, start, end, transient, nil, lm)

	// Step 11.2: Encode spread decision
	const spreadNormal = 1
	re.EncodeICDF(spreadNormal, spreadICDF, 5)

	// Step 11.3: Initialize caps for allocation
	caps := initCaps(nbBands, lm, e.channels)

	// Step 11.4: Encode dynamic allocation
	offsets := make([]int, nbBands)
	dynallocLogp := 6
	for i := start; i < end; i++ {
		re.EncodeBit(0, uint(dynallocLogp))
	}

	// Step 11.5: Encode allocation trim
	const allocTrim = 5
	re.EncodeICDF(allocTrim, trimICDF, 7)

	// Step 12: Compute bit allocation
	bitsUsed := re.TellFrac()
	totalBitsQ3 := (targetBits << bitRes) - bitsUsed - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && totalBitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	totalBitsQ3 -= antiCollapseRsv

	allocResult := ComputeAllocationWithEncoder(
		re,
		totalBitsQ3>>bitRes,
		nbBands,
		e.channels,
		caps,
		offsets,
		allocTrim,
		intensity,
		dualStereo,
		lm,
	)

	// Step 13: Encode fine energy
	e.EncodeFineEnergy(energies, quantizedEnergies, nbBands, allocResult.FineBits)

	// Step 13.5: Normalize bands using QUANTIZED energies
	var shapesL, shapesR [][]float64
	if e.channels == 1 {
		shapesL = e.NormalizeBands(mdctCoeffs, quantizedEnergies, nbBands, frameSize)
	} else {
		// Split energies for L and R
		energiesL := quantizedEnergies[:nbBands]
		energiesR := quantizedEnergies[nbBands:]
		shapesL = e.NormalizeBands(mdctLeft, energiesL, nbBands, frameSize)
		shapesR = e.NormalizeBands(mdctRight, energiesR, nbBands, frameSize)
	}

	// Step 14: Encode bands (PVQ)
	e.EncodeBands(shapesL, shapesR, allocResult.BandBits, nbBands, frameSize)

	// Step 15: Finalize and update state
	bytes := re.Done()
	e.SetPrevEnergyWithPrev(prev1LogE, quantizedEnergies)
	e.frameCount++

	return bytes, nil
}

// computeMDCTForEncoding computes MDCT for encoding with proper windowing.
// For transient mode, uses multiple short MDCTs.
// Note: This is a stateless version that uses zero-padding for the first half.
// For proper overlap handling, use computeMDCTWithOverlap which uses the encoder's state.
func computeMDCTForEncoding(samples []float64, frameSize, shortBlocks int) []float64 {
	if len(samples) == 0 {
		return nil
	}

	overlap := Overlap
	if overlap > len(samples) {
		overlap = len(samples)
	}

	input := make([]float64, len(samples)+overlap)
	copy(input[overlap:], samples)

	if shortBlocks > 1 {
		return MDCTShort(input, shortBlocks)
	}
	return MDCT(input)
}

// computeMDCTWithOverlap computes MDCT using the encoder's overlap buffer for continuity.
// This ensures proper MDCT overlap-add analysis across frame boundaries.
func (e *Encoder) computeMDCTWithOverlap(samples []float64, shortBlocks int) []float64 {
	overlap := Overlap
	if overlap > len(samples) {
		overlap = len(samples)
	}
	if len(e.overlapBuffer) < overlap {
		newBuf := make([]float64, overlap)
		copy(newBuf, e.overlapBuffer)
		e.overlapBuffer = newBuf
	}

	return ComputeMDCTWithHistory(samples, e.overlapBuffer[:overlap], shortBlocks)
}

// ComputeMDCTWithHistory computes MDCT using a history buffer for overlap.
// samples: current frame samples
// history: buffer containing previous frame's tail (will be updated with current frame's tail)
// shortBlocks: number of short blocks for transient mode
func ComputeMDCTWithHistory(samples, history []float64, shortBlocks int) []float64 {
	if len(samples) == 0 {
		return nil
	}

	overlap := Overlap
	if overlap > len(samples) {
		overlap = len(samples)
	}
	input := make([]float64, len(samples)+overlap)

	// Copy history overlap into the head of the input buffer.
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(input[:overlap], history[len(history)-overlap:])
		} else {
			copy(input[overlap-len(history):overlap], history)
		}
	}

	// Append current frame samples after the overlap.
	copy(input[overlap:], samples)

	// Update history with the current frame tail (overlap samples).
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(history, samples[len(samples)-overlap:])
		} else {
			copy(history, samples[len(samples)-len(history):])
		}
	}

	if shortBlocks > 1 {
		return MDCTShort(input, shortBlocks)
	}
	return MDCT(input)
}

// isFrameSilent checks if all samples are effectively zero.
func isFrameSilent(pcm []float64) bool {
	const silenceThreshold = 1e-10

	for _, s := range pcm {
		if s > silenceThreshold || s < -silenceThreshold {
			return false
		}
	}
	return true
}

// isIntraFrame returns true if this frame should use intra mode.
// Intra mode is used for the first frame or after a reset.
func (e *Encoder) isIntraFrame() bool {
	return e.frameCount == 0
}

// frameCount tracks encoded frames for intra mode decisions.
// Added to Encoder struct implicitly via closure or needs to be added.

// EncodeFrameWithOptions encodes a frame with additional control options.
func (e *Encoder) EncodeFrameWithOptions(pcm []float64, frameSize int, opts EncodeOptions) ([]byte, error) {
	// Apply options
	if opts.ForceIntra {
		// Temporarily set frame count to 0 for intra mode
		savedCount := e.frameCount
		e.frameCount = 0
		defer func() { e.frameCount = savedCount }()
	}

	return e.EncodeFrame(pcm, frameSize)
}

// EncodeOptions provides encoding control options.
type EncodeOptions struct {
	ForceIntra     bool // Force intra mode (no inter-frame prediction)
	ForceTransient bool // Force transient mode (short blocks)
	Bitrate        int  // Target bitrate in bits per second (0 = default)
}

// EncodeStereoFrame encodes a stereo frame from separate L/R channels.
// left: left channel samples
// right: right channel samples
// frameSize: 120, 240, 480, or 960 samples per channel
func (e *Encoder) EncodeStereoFrame(left, right []float64, frameSize int) ([]byte, error) {
	if e.channels != 2 {
		return nil, errors.New("celt: encoder not configured for stereo")
	}

	if len(left) != frameSize || len(right) != frameSize {
		return nil, ErrInvalidInputLength
	}

	// Interleave for standard encoding path
	interleaved := InterleaveStereo(left, right)
	return e.EncodeFrame(interleaved, frameSize)
}

// computeTargetBits computes the target bit budget based on bitrate and frame size.
// For 64kbps and 20ms frame: 64000 * 20 / 1000 = 1280 bits = 160 bytes
func (e *Encoder) computeTargetBits(frameSize int) int {
	if e.targetBitrate <= 0 {
		// Default fallback: use a reasonable estimate based on frame size
		return frameSize * 4 // ~32kbps default
	}
	// frameDurationMs = frameSize * 1000 / 48000
	// targetBits = bitrate * frameDuration / 1000
	// Simplified: targetBits = bitrate * frameSize / 48000
	return e.targetBitrate * frameSize / 48000
}
