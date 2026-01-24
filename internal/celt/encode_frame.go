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

	// Step 4: Apply pre-emphasis
	preemph := e.ApplyPreemphasis(pcm)

	// Step 5: Compute MDCT with proper overlap handling
	var mdctCoeffs []float64
	if e.channels == 1 {
		// Mono: MDCT directly with overlap buffer for continuity
		mdctCoeffs = e.computeMDCTWithOverlap(preemph, shortBlocks)
	} else {
		// Stereo: convert to mid-side, then MDCT each
		left, right := DeinterleaveStereo(preemph)
		mid, side := ConvertToMidSide(left, right)

		// Use overlap-aware MDCT for both channels
		// Note: For stereo, we'd ideally have separate overlap buffers per channel
		// For now, use the stateless version which still improves mono path
		mdctMid := computeMDCTForEncoding(mid, frameSize, shortBlocks)
		mdctSide := computeMDCTForEncoding(side, frameSize, shortBlocks)

		// Concatenate: [mid coeffs][side coeffs]
		mdctCoeffs = make([]float64, len(mdctMid)+len(mdctSide))
		copy(mdctCoeffs[:len(mdctMid)], mdctMid)
		copy(mdctCoeffs[len(mdctMid):], mdctSide)
	}

	// Step 6: Compute band energies
	energies := e.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Step 7: Normalize bands
	shapes := e.NormalizeBands(mdctCoeffs, energies, nbBands, frameSize)

	// Step 8: Initialize range encoder with bitrate-derived size
	// Compute target bits from bitrate
	targetBits := e.computeTargetBits(frameSize)
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	e.SetRangeEncoder(re)

	// Step 9: Encode frame flags

	// Check if this is a silence frame
	isSilence := isFrameSilent(pcm)
	if isSilence {
		// Encode silence flag = 1
		re.EncodeBit(1, 15) // High probability bit for silence
		// For silence frames, we just return minimal packet
		bytes := re.Done()
		return bytes, nil
	}

	// Encode silence flag = 0 (not silent)
	re.EncodeBit(0, 15)

	// Note: At complexity 0, libopus might skip the postfilter entirely.
	// The postfilter flag (start=0 && budget sufficient) adds overhead.
	// We encode postfilter=0 (disabled) per RFC 6716 Section 4.3.3.1.
	start := 0
	re.EncodeBit(0, 1) // Postfilter disabled

	// Encode transient flag (only for LM > 0, per decoder.go:357)
	if lm > 0 {
		var transientBit int
		if transient {
			transientBit = 1
		}
		re.EncodeBit(transientBit, 3) // P(transient) = 1/8
	}

	// Encode intra flag
	// First frame or after reset uses intra mode (no inter-frame prediction)
	intra := e.isIntraFrame()
	var intraBit int
	if intra {
		intraBit = 1
	}
	re.EncodeBit(intraBit, 3) // P(intra) = 1/8

	// Step 10: For stereo frames, encode stereo params
	intensity := -1
	dualStereo := false
	if e.channels == 2 {
		intensity = e.EncodeStereoParams(nbBands)
	}

	// Step 11: Encode coarse energy
	quantizedEnergies := e.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	// Step 11.1: Encode TF (time-frequency) resolution
	// Per decoder.go:379 - TF encoding happens after coarse energy
	end := nbBands
	tfEncode(re, start, end, transient, nil, lm)

	// Step 11.2: Encode spread decision
	// Per decoder.go:381-385 - spread is encoded using spreadICDF
	// SPREAD_NORMAL = 1 is the typical value
	const spreadNormal = 1
	re.EncodeICDF(spreadNormal, spreadICDF, 5)

	// Step 11.3: Initialize caps for allocation
	caps := initCaps(nbBands, lm, e.channels)

	// Step 11.4: Encode dynamic allocation (all zeros = no boost)
	// Per decoder.go:388-411
	offsets := make([]int, nbBands)
	dynallocLogp := 6
	for i := start; i < end; i++ {
		// Encode dynalloc = 0 (no boost) for this band
		re.EncodeBit(0, uint(dynallocLogp))
		// dynallocLogp stays at 6 when no boost is applied
	}

	// Step 11.5: Encode allocation trim
	// Per decoder.go:413-416 - trim = 5 is neutral
	const allocTrim = 5
	re.EncodeICDF(allocTrim, trimICDF, 7)

	// Step 12: Compute bit allocation
	// Use bitrate-derived target bits
	bitsUsed := re.TellFrac()
	totalBitsQ3 := (targetBits << bitRes) - bitsUsed - 1

	// Reserve anti-collapse bit if needed
	antiCollapseRsv := 0
	if transient && lm >= 2 && totalBitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	totalBitsQ3 -= antiCollapseRsv

	allocResult := ComputeAllocation(
		totalBitsQ3>>bitRes, // Convert Q3 back to bits
		nbBands,
		e.channels,
		caps,
		offsets,
		allocTrim,
		intensity, // intensity band (-1 = disabled)
		dualStereo,
		lm,
	)

	// Step 13: Encode fine energy
	e.EncodeFineEnergy(energies, quantizedEnergies, nbBands, allocResult.FineBits)

	// Step 14: Encode bands (PVQ)
	e.EncodeBands(shapes, allocResult.BandBits, nbBands, frameSize)

	// Step 15: Finalize and update state
	bytes := re.Done()

	// Update previous energy for next frame's inter-frame prediction
	e.SetPrevEnergy(quantizedEnergies)

	// Mark that we've encoded a frame (for intra flag logic)
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

	// MDCT expects 2*N samples to produce N coefficients
	n := len(samples)
	padded := make([]float64, n*2)
	// First half is zeros for first frame
	// Second half is current frame
	copy(padded[n:], samples)

	if shortBlocks > 1 {
		return MDCTShort(padded, shortBlocks)
	}
	return MDCT(padded)
}

// computeMDCTWithOverlap computes MDCT using the encoder's overlap buffer for continuity.
// This ensures proper MDCT overlap-add analysis across frame boundaries.
func (e *Encoder) computeMDCTWithOverlap(samples []float64, shortBlocks int) []float64 {
	if len(samples) == 0 {
		return nil
	}

	n := len(samples)
	padded := make([]float64, n*2)

	// First half is previous frame's tail (from overlap buffer)
	// This ensures MDCT continuity across frames
	overlapLen := len(e.overlapBuffer)
	if overlapLen > 0 {
		if overlapLen > n {
			// Copy the last n samples from overlap buffer
			copy(padded[:n], e.overlapBuffer[overlapLen-n:])
		} else {
			// Copy all of overlap buffer, offset to align at position n
			copy(padded[n-overlapLen:n], e.overlapBuffer)
		}
	}

	// Second half is current frame
	copy(padded[n:], samples)

	// Save current frame's tail for next frame's overlap
	if len(e.overlapBuffer) < n {
		e.overlapBuffer = make([]float64, n)
	}
	copy(e.overlapBuffer, samples)

	if shortBlocks > 1 {
		return MDCTShort(padded, shortBlocks)
	}
	return MDCT(padded)
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
