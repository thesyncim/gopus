// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the complete frame encoding pipeline.

package celt

import (
	"errors"
	"math"

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
	bandE := make([]float64, nbBands*e.channels)
	for c := 0; c < e.channels; c++ {
		for band := 0; band < nbBands; band++ {
			idx := c*nbBands + band
			if idx >= len(energies) {
				continue
			}
			eVal := energies[idx]
			if band < len(eMeans) {
				eVal += eMeans[band] * DB6
			}
			if eVal > 32*DB6 {
				eVal = 32 * DB6
			}
			bandE[idx] = math.Exp2(eVal / DB6)
		}
	}

	// Note: Band normalization uses the ORIGINAL energies (libopus normalise_bands).
	// Quantized energies are used only for decoding/denormalization.

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
	intra := e.IsIntraFrame()
	var intraBit int
	if intra {
		intraBit = 1
	}
	re.EncodeBit(intraBit, 3)

	// Step 10: Prepare stereo params (encoded during allocation)
	// Use mid-side stereo by default: dualStereo=false, intensity=nbBands (disabled)
	intensity := nbBands
	dualStereo := false

	// Step 11: Encode coarse energy
	prev1LogE := append([]float64(nil), e.prevEnergy...)
	quantizedEnergies := e.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	// Step 11.0.5: Normalize bands early for TF analysis
	// TF analysis needs normalized coefficients to determine optimal time-frequency resolution
	var normL, normR []float64
	if e.channels == 1 {
		normL = e.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)
	} else {
		energiesL := energies[:nbBands]
		energiesR := energies[nbBands:]
		normL = e.NormalizeBandsToArray(mdctLeft, energiesL, nbBands, frameSize)
		normR = e.NormalizeBandsToArray(mdctRight, energiesR, nbBands, frameSize)
	}

	// Step 11.1: Compute and encode TF (time-frequency) resolution
	end := nbBands
	effectiveBytes := targetBits / 8

	// Enable TF analysis when we have enough bits, not in hybrid mode, and reasonable complexity
	// Reference: libopus enable_tf_analysis = effectiveBytes>=15*C && !hybrid && st->complexity>=2 && !st->lfe
	enableTFAnalysis := effectiveBytes >= 15*e.channels && lm > 0

	var tfRes []int
	var tfSelect int

	if enableTFAnalysis {
		// Compute TF estimate based on transient detection
		// 0.0 = favors time resolution, 1.0 = favors frequency resolution
		var tfEstimate float64
		if transient {
			tfEstimate = 0.2 // Transients favor time resolution
		} else {
			tfEstimate = 0.5 // Default balanced
		}

		// Use the normalized coefficients for TF analysis
		// For stereo, use the left channel (similar to libopus tf_chan approach)
		tfRes, tfSelect = TFAnalysis(normL, len(normL), nbBands, transient, lm, tfEstimate, effectiveBytes, nil)

		// Encode TF decisions using the computed values
		TFEncodeWithSelect(re, start, end, transient, tfRes, lm, tfSelect)
	} else {
		// Use default TF settings when analysis is disabled
		tfRes = make([]int, nbBands)
		tfSelect = 0
		if transient {
			// For transients without analysis, use tf_res=1 (favor time resolution)
			for i := 0; i < nbBands; i++ {
				tfRes[i] = 1
			}
		}
		tfEncode(re, start, end, transient, tfRes, lm)

		// Convert tfRes to actual TF change values
		for i := start; i < end; i++ {
			idx := 4*boolToInt(transient) + 2*tfSelect + tfRes[i]
			tfRes[i] = int(tfSelectTable[lm][idx])
		}
	}

	// Step 11.2: Compute and encode spread decision
	// For transient frames (shortBlocks > 1), use SPREAD_NORMAL without analysis
	// For normal frames, analyze spectral characteristics to decide spread
	var spread int
	if shortBlocks > 1 {
		// Transient mode: use SPREAD_NORMAL (libopus behavior for hybrid/short blocks)
		spread = spreadNormal
	} else {
		// Normal mode: analyze normalized coefficients for spread decision
		// The normalized coefficients are computed after coarse energy,
		// but before encoding we need to use pre-normalized coefficients
		// scaled by the actual energy for proper analysis.
		// For now, use the normalized array which will be computed.
		// Note: In libopus, spreading_decision uses normalized X after normalise_bands
		spread = e.SpreadingDecision(normL, nbBands, e.channels, frameSize, true)
	}
	re.EncodeICDF(spread, spreadICDF, 5)

	// Step 11.3: Initialize caps for allocation
	caps := initCaps(nbBands, lm, e.channels)

	// Step 11.4: Encode dynamic allocation
	// Match libopus/decoder: check budget before encoding each dynalloc bit.
	// Since we don't use dynalloc boosts (offsets are all 0), we just encode
	// one 0-bit per band IF budget allows, matching what decoder expects.
	offsets := make([]int, nbBands)
	dynallocLogp := 6
	totalBitsQ3ForDynalloc := targetBits << bitRes
	tellFracDynalloc := re.TellFrac()
	for i := start; i < end; i++ {
		// Only encode if budget allows (matching decoder's budget check)
		if tellFracDynalloc+(dynallocLogp<<bitRes) < totalBitsQ3ForDynalloc {
			re.EncodeBit(0, uint(dynallocLogp))
			tellFracDynalloc = re.TellFrac()
		}
	}

	// Step 11.5: Encode allocation trim (only if budget allows)
	allocTrim := 5
	tellForTrim := re.TellFrac()
	if tellForTrim+(6<<bitRes) <= totalBitsQ3ForDynalloc {
		re.EncodeICDF(allocTrim, trimICDF, 7)
	}

	// Step 12: Compute bit allocation
	bitsUsed := re.TellFrac()
	totalBitsQ3 := (targetBits << bitRes) - bitsUsed - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && totalBitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	totalBitsQ3 -= antiCollapseRsv

	signalBandwidth := nbBands - 1
	if signalBandwidth < 0 {
		signalBandwidth = 0
	}

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
		e.lastCodedBands,
		signalBandwidth,
	)
	if e.lastCodedBands != 0 {
		e.lastCodedBands = minInt(e.lastCodedBands+1, maxInt(e.lastCodedBands-1, allocResult.CodedBands))
	} else {
		e.lastCodedBands = allocResult.CodedBands
	}

	// Step 13: Encode fine energy
	e.EncodeFineEnergy(energies, quantizedEnergies, nbBands, allocResult.FineBits)

	// Note: normL/normR and tfRes were already computed before encoding spread
	// to ensure the same normalized coefficients are used for analysis and quantization

	// Step 14: Encode bands (quant_all_bands)
	totalBitsAllQ3 := (targetBits << bitRes) - antiCollapseRsv
	dualStereoVal := 0
	if allocResult.DualStereo {
		dualStereoVal = 1
	}
	quantAllBandsEncode(
		re,
		e.channels,
		frameSize,
		lm,
		start,
		end,
		normL,
		normR,
		allocResult.BandBits,
		shortBlocks,
		spread, // Use computed spread decision instead of hardcoded spreadNormal
		dualStereoVal,
		allocResult.Intensity,
		tfRes,
		totalBitsAllQ3,
		allocResult.Balance,
		allocResult.CodedBands,
		&e.rng,
		bandE,
		nil,
		nil,
	)

	// Step 14.5: Encode anti-collapse flag if reserved
	if antiCollapseRsv > 0 {
		re.EncodeRawBits(0, 1)
	}

	// Step 14.6: Encode energy finalization bits (leftover budget)
	bitsLeft := targetBits - re.Tell()
	if bitsLeft < 0 {
		bitsLeft = 0
	}
	e.EncodeEnergyFinalise(energies, quantizedEnergies, nbBands, allocResult.FineBits, allocResult.FinePriority, bitsLeft)

	// Step 15: Finalize and update state
	bytes := re.Done()
	e.SetPrevEnergyWithPrev(prev1LogE, quantizedEnergies)
	e.IncrementFrameCount()

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
	bitrate := e.targetBitrate
	if bitrate <= 0 {
		if e.channels == 2 {
			bitrate = 128000
		} else {
			bitrate = 64000
		}
	}
	if bitrate < 6000 {
		bitrate = 6000
	}
	if bitrate > 510000 {
		bitrate = 510000
	}
	// frameDurationMs = frameSize * 1000 / 48000
	// targetBits = bitrate * frameDuration / 1000
	// Simplified: targetBits = bitrate * frameSize / 48000
	return bitrate * frameSize / 48000
}
