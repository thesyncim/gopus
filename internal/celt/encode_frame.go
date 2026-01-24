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

	// Step 5: Compute MDCT
	var mdctCoeffs []float64
	if e.channels == 1 {
		// Mono: MDCT directly
		// MDCT expects 2*frameSize input for overlap, but we have frameSize
		// For encoding, we use the frame directly with windowing
		mdctCoeffs = computeMDCTForEncoding(preemph, frameSize, shortBlocks)
	} else {
		// Stereo: convert to mid-side, then MDCT each
		left, right := DeinterleaveStereo(preemph)
		mid, side := ConvertToMidSide(left, right)

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

	// Step 8: Initialize range encoder
	// Allocate buffer for output (generous size)
	bufSize := frameSize / 2 // Typical CELT packet size
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

	// Encode transient flag (only for LM >= 1)
	if lm >= 1 {
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

	// Step 12: Compute bit allocation
	// Estimate total bits available (conservative estimate)
	bitsUsed := re.Tell()
	totalBits := bufSize*8 - bitsUsed
	if totalBits < 0 {
		totalBits = 64
	}

	allocResult := ComputeAllocation(
		totalBits,
		nbBands,
		e.channels,
		nil,       // caps
		nil,       // dynalloc
		0,         // trim (neutral)
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
func computeMDCTForEncoding(samples []float64, frameSize, shortBlocks int) []float64 {
	if len(samples) == 0 {
		return nil
	}

	// MDCT expects 2*N samples to produce N coefficients
	// For encoding, we need to handle the overlap properly
	// Pad with zeros for the first frame (or use overlap buffer)
	n := len(samples)
	padded := make([]float64, n*2)
	// First half is zeros (or would be previous frame overlap)
	// Second half is current frame
	copy(padded[n:], samples)

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
