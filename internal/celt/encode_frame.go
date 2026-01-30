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

	// Step 3a: Apply DC rejection (high-pass filter) to remove DC offset
	// libopus applies this at the Opus encoder level before CELT processing
	// Reference: libopus src/opus_encoder.c line 2008: dc_reject(pcm, 3, ...)
	dcRejected := e.ApplyDCReject(pcm)

	// Step 3b: Apply delay buffer (lookahead compensation)
	// libopus uses a delay_compensation of Fs/250 = 192 samples at 48kHz.
	// The delay buffer is prepended to the new samples, creating a lookahead.
	// Reference: libopus src/opus_encoder.c line 1967
	delayComp := DelayCompensation * e.channels
	if len(e.delayBuffer) < delayComp {
		e.delayBuffer = make([]float64, delayComp)
	}

	// Build combined buffer: [delay_buffer] + [DC-rejected new samples]
	combinedLen := delayComp + len(dcRejected)
	combinedBuf := make([]float64, combinedLen)
	copy(combinedBuf[:delayComp], e.delayBuffer)
	copy(combinedBuf[delayComp:], dcRejected)

	// Take frame_size samples from the start for processing
	samplesForFrame := combinedBuf[:expectedLen]

	// Save remaining samples to delay buffer for next frame
	// The tail of combinedBuf (last delayComp samples) becomes new delay buffer
	delayTailStart := len(combinedBuf) - delayComp
	copy(e.delayBuffer, combinedBuf[delayTailStart:])

	// Step 3c: Apply pre-emphasis with signal scaling (before transient analysis)
	// This matches libopus order: celt_preemphasis() is called before transient_analysis()
	// Reference: libopus celt_encoder.c lines 2015-2030
	// Input samples in float range [-1.0, 1.0] are scaled to signal scale (x32768)
	// This matches libopus CELT_SIG_SCALE. The decoder reverses this with scaleSamples(1/32768).
	preemph := e.ApplyPreemphasisWithScaling(samplesForFrame)

	// Step 4: Detect transient and compute tf_estimate using PRE-EMPHASIZED signal
	// libopus calls transient_analysis(in, N+overlap, ...) where 'in' contains:
	// - Previous frame's pre-emphasized overlap samples (indices 0 to overlap-1)
	// - Current frame's pre-emphasized samples (indices overlap to overlap+N-1)
	// Reference: libopus celt_encoder.c line 2030
	overlap := Overlap
	if overlap > frameSize {
		overlap = frameSize
	}

	// Ensure preemphBuffer is properly sized
	preemphBufSize := overlap * e.channels
	if len(e.preemphBuffer) < preemphBufSize {
		e.preemphBuffer = make([]float64, preemphBufSize)
	}

	// Build combined signal for transient analysis: [overlap from previous frame] + [current frame]
	// Total length: (overlap + frameSize) * channels
	transientInput := make([]float64, (overlap+frameSize)*e.channels)
	copy(transientInput[:preemphBufSize], e.preemphBuffer[:preemphBufSize])
	copy(transientInput[preemphBufSize:], preemph)

	// Call transient analysis with the pre-emphasized signal (N+overlap samples)
	transientResult := e.TransientAnalysis(transientInput, frameSize+overlap, false /* allowWeakTransients */)
	transient := transientResult.IsTransient
	tfEstimate := transientResult.TfEstimate
	toneishness := transientResult.Toneishness

	// Match libopus line 2033: cap toneishness based on tf_estimate
	// libopus: toneishness = MIN32(toneishness, QCONST32(1.f, 29)-SHL32(tf_estimate, 15))
	// In float: toneishness = min(toneishness, 1.0 - tf_estimate)
	maxToneishness := 1.0 - tfEstimate
	if toneishness > maxToneishness {
		toneishness = maxToneishness
	}

	// Allow force transient override for testing (matches libopus first frame behavior)
	if e.forceTransient {
		transient = true
	}

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Save current frame's tail (last overlap samples) for next frame's transient analysis
	// For mono: last 'overlap' samples
	// For stereo: last 'overlap' interleaved pairs
	tailStart := len(preemph) - preemphBufSize
	if tailStart >= 0 {
		copy(e.preemphBuffer[:preemphBufSize], preemph[tailStart:])
	}

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
	// Match libopus encoding order and budget conditions exactly:
	// 1. Silence flag (if tell==1, with logp=15)
	// 2. Postfilter flag (if start==0 && !hybrid && tell+16<=total_bits, with logp=1)
	// 3. Transient flag (if LM>0 && tell+3<=total_bits, with logp=3)
	// 4. Intra energy flag (if tell+3<=total_bits, with logp=3)
	//
	// Reference: libopus celt_encoder.c lines 1981-1984:
	//   if (tell==1)
	//      ec_enc_bit_logp(enc, silence, 15);
	//   else
	//      silence=0;
	// The silence flag is ONLY encoded when tell==1 (the very first bit position).
	// If tell!=1, silence is forced to 0 without encoding anything.
	isSilence := isFrameSilent(pcm)
	tell := re.Tell()
	if tell == 1 {
		if isSilence {
			re.EncodeBit(1, 15)
			// Capture final range BEFORE Done(), matching libopus celt_encoder.c:2809
			e.rng = re.Range()
			bytes := re.Done()
			return bytes, nil
		}
		re.EncodeBit(0, 15)
	} else {
		// tell != 1: don't encode silence flag, force silence to false
		isSilence = false
	}
	start := 0

	// Postfilter flag: only encode if start==0, NOT in hybrid mode, and budget allows
	// Reference: libopus celt_encoder.c line 2047-2048
	// if(!hybrid && tell+16<=total_bits) ec_enc_bit_logp(enc, 0, 1);
	if !e.IsHybrid() && start == 0 && re.Tell()+16 <= targetBits {
		re.EncodeBit(0, 1) // No postfilter (disabled for now)
	}

	// Transient flag: only encode if LM>0 and budget allows
	// Reference: libopus celt_encoder.c line 2063-2069
	// if (LM>0 && ec_tell(enc)+3<=total_bits)
	if lm > 0 && re.Tell()+3 <= targetBits {
		var transientBit int
		if transient {
			transientBit = 1
		}
		re.EncodeBit(transientBit, 3)
	} else if lm > 0 {
		// Budget doesn't allow transient flag, force non-transient
		transient = false
		shortBlocks = 1
	}

	// Intra energy flag: only encode if budget allows
	// Reference: libopus celt_decoder.c line 1377
	// intra_ener = tell+3<=total_bits ? ec_dec_bit_logp(dec, 3) : 0
	intra := e.IsIntraFrame()
	if re.Tell()+3 <= targetBits {
		var intraBit int
		if intra {
			intraBit = 1
		}
		re.EncodeBit(intraBit, 3)
	} else {
		// Budget doesn't allow intra flag, force inter mode
		intra = false
	}

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

	// Step 11.0.6: Compute tonality analysis for next frame's VBR decisions
	// We compute tonality here using Spectral Flatness Measure (SFM) and store it
	// for use in the next frame's computeVBRTarget (similar to how libopus uses
	// analysis from the previous frame).
	e.updateTonalityAnalysis(normL, energies, nbBands, frameSize)

	// Step 11.1: Compute and encode TF (time-frequency) resolution
	end := nbBands
	effectiveBytes := targetBits / 8

	// Step 11.0.7: Compute dynalloc analysis for VBR and bit allocation
	// This computes maxDepth, offsets, importance, and spread_weight.
	// The results are stored for next frame's VBR target computation.
	// Reference: libopus celt/celt_encoder.c dynalloc_analysis()
	lsbDepth := 16 // Assume 16-bit input; could be made configurable
	logN := make([]int16, nbBands)
	for i := 0; i < nbBands && i < len(LogN); i++ {
		logN[i] = int16(LogN[i])
	}
	// Determine VBR mode (simplified: assume VBR if targetBitrate is set)
	isVBR := e.targetBitrate > 0
	isConstrainedVBR := false // TODO: Add constrained VBR support
	dynallocResult := DynallocAnalysis(
		energies, energies, e.prevEnergy,
		nbBands, start, end, e.channels, lsbDepth, lm,
		logN,
		effectiveBytes,
		transient, isVBR, isConstrainedVBR,
	)
	// Store for next frame's VBR computation
	e.lastDynalloc = dynallocResult

	// Enable TF analysis when we have enough bits and reasonable complexity.
	// Reference: libopus enable_tf_analysis = effectiveBytes>=15*C && !hybrid && st->complexity>=2 && !st->lfe && toneishness < QCONST32(.98f, 29)
	// Note: libopus does NOT have an LM>0 check here - TF analysis runs for all frame sizes including LM=0
	// CRITICAL: toneishness >= 0.98 disables TF analysis (pure tones use simple fallback)
	enableTFAnalysis := effectiveBytes >= 15*e.channels && e.complexity >= 2 && toneishness < 0.98

	var tfRes []int
	var tfSelect int

	if enableTFAnalysis {
		// tf_estimate was computed by TransientAnalysis using libopus algorithm
		// It measures temporal variation: 0.0 = steady (favor freq), 1.0 = transient (favor time)
		// Note: For forced transient mode (e.g., hybrid weak transients), override to 0.2
		useTfEstimate := tfEstimate
		if transient && tfEstimate < 0.2 {
			// Ensure transient frames have at least minimal time-favoring bias
			useTfEstimate = 0.2
		}

		// Use importance from dynalloc analysis for TF decision weighting
		// This weights perceptually important bands higher in the Viterbi search
		// Reference: libopus celt/celt_encoder.c dynalloc_analysis() -> importance
		importance := dynallocResult.Importance

		// Use the normalized coefficients for TF analysis
		// For stereo, use the left channel (similar to libopus tf_chan approach)
		tfRes, tfSelect = TFAnalysis(normL, len(normL), nbBands, transient, lm, useTfEstimate, effectiveBytes, importance)

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
	// Match libopus gating: only encode if there's budget for the decision.
	var spread int
	if re.Tell()+4 <= targetBits {
		if shortBlocks > 1 || e.complexity < 3 || effectiveBytes < 10*e.channels {
			if e.complexity == 0 {
				spread = spreadNone
			} else {
				spread = spreadNormal
			}
			// Reset tapset decision when spread analysis is skipped
			// Reference: libopus celt_encoder.c line 2306: st->tapset_decision = 0
			e.SetTapsetDecision(0)
		} else {
			// Analyze normalized coefficients for spread decision.
			// Note: libopus uses normalized X after normalise_bands.
			// Enable updateHF when prefilter could be active (non-short blocks).
			// The tapset decision is used for the prefilter comb filter taper selection.
			// Reference: libopus celt_encoder.c spreading_decision() call with
			// pf_on&&!shortBlocks as updateHF condition.
			// Since we don't have full prefilter yet, we enable HF update
			// when conditions are met for future prefilter integration.
			updateHF := shortBlocks == 1
			// Compute dynamic spread weights based on masking analysis (matches libopus dynalloc_analysis)
			// Using 16-bit depth assumption (standard audio input)
			spreadWeights := ComputeSpreadWeights(energies, nbBands, e.channels, 16)
			spread = e.SpreadingDecisionWithWeights(normL, nbBands, e.channels, frameSize, updateHF, spreadWeights)
		}
		re.EncodeICDF(spread, spreadICDF, 5)
	} else {
		spread = spreadNormal
	}

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
	// The tapset decision is computed during spreading_decision and used here
	// for state tracking. While tapset primarily affects the comb filter
	// (prefilter/postfilter), it's tracked in the quantization context for
	// encoder state consistency and future prefilter integration.
	totalBitsAllQ3 := (targetBits << bitRes) - antiCollapseRsv
	dualStereoVal := 0
	if allocResult.DualStereo {
		dualStereoVal = 1
	}
	tapset := e.TapsetDecision()
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
		spread, // Spreading parameter for PVQ rotation
		tapset, // Tapset decision for comb filter taper (tracked for state)
		dualStereoVal,
		allocResult.Intensity,
		tfRes,
		totalBitsAllQ3,
		allocResult.Balance,
		allocResult.CodedBands,
		&e.rng,
		e.complexity,
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
	// Capture final range BEFORE Done(), matching libopus celt_encoder.c:2809
	// This is critical for verification - the final range must be captured before
	// ec_enc_done() flushes the remaining bytes.
	e.rng = re.Range()
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
// This implements libopus VBR logic from celt_encoder.c compute_vbr().
//
// For 64kbps and 20ms frame with VBR:
// - Base: bitrate * frameSize / 48000 = 1280 bits
// - VBR can boost up to 2x based on signal characteristics
//
// Reference: libopus celt/celt_encoder.c compute_vbr() and VBR computation around line 2435
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

	// Compute base bits using libopus bitrate_to_bits formula
	// bitrate_to_bits(bitrate, Fs, frame_size) = bitrate*6/(6*Fs/frame_size)
	// For 48kHz: = bitrate * 6 / (6 * 48000 / frameSize)
	//           = bitrate * frameSize / 48000
	baseBits := bitrate * frameSize / 48000

	// Convert to Q3 format (8ths of bits) for VBR computation
	// Reference: libopus celt_encoder.c line 1903
	vbrRateQ3 := baseBits << bitRes

	// Compute base_target with overhead subtraction
	// Reference: libopus celt_encoder.c line 2448
	// base_target = vbr_rate - ((40*C+20)<<BITRES)
	overheadQ3 := (40*e.channels + 20) << bitRes
	baseTargetQ3 := vbrRateQ3 - overheadQ3
	if baseTargetQ3 < 0 {
		baseTargetQ3 = 0
	}

	// For VBR mode, apply boost based on signal characteristics
	// This is a simplified version of libopus compute_vbr()
	targetQ3 := e.computeVBRTarget(baseTargetQ3, frameSize, bitrate)

	// Convert back from Q3 to bits
	// Reference: libopus line 2480: nbAvailableBytes = (target+(1<<(BITRES+2)))>>(BITRES+3)
	// For bits (not bytes): target_bits = (targetQ3 + 4) >> 3
	targetBits := (targetQ3 + (1 << (bitRes - 1))) >> bitRes

	// Clamp to reasonable bounds
	// Minimum: 2 bytes (16 bits)
	// Maximum: 1275 bytes * 8 = 10200 bits (max Opus packet)
	if targetBits < 16 {
		targetBits = 16
	}
	maxBits := 1275 * 8
	if e.channels == 1 && frameSize == 960 {
		// For mono 20ms, cap at ~320 bytes (reasonable VBR max for 64kbps)
		maxBits = baseBits * 2 // Up to 2x boost
	}
	if targetBits > maxBits {
		targetBits = maxBits
	}

	return targetBits
}

// computeVBRTarget applies VBR boosting to the base target.
// This mirrors libopus compute_vbr() from celt_encoder.c lines 1604-1716.
//
// Key boosts applied:
// - tot_boost: dynalloc analysis boost from previous frame
// - tf_estimate: transient boost (from TransientAnalysis)
// - tonality: tonal signal boost (approximated from spectrum analysis)
// - floor_depth: signal depth floor based on maxDepth from dynalloc
//
// All values are in Q3 format (8ths of bits).
func (e *Encoder) computeVBRTarget(baseTargetQ3, frameSize, bitrate int) int {
	mode := GetModeConfig(frameSize)
	lm := mode.LM
	nbBands := mode.EffBands

	// Compute coded_bins: number of MDCT bins being coded
	// Reference: libopus line 1623: coded_bins = eBands[coded_bands]<<LM
	codedBands := nbBands
	if e.lastCodedBands > 0 && e.lastCodedBands < nbBands {
		codedBands = e.lastCodedBands
	}
	codedBins := EBands[codedBands] << lm
	if e.channels == 2 {
		// For stereo, add intensity stereo bins
		// Reference: libopus line 1625: coded_bins += eBands[IMIN(intensity, coded_bands)]<<LM
		intensityBand := codedBands // Default: no intensity stereo
		codedBins += EBands[intensityBand] << lm
	}

	targetQ3 := baseTargetQ3

	// Apply dynalloc boost (tot_boost) minus calibration
	// Reference: libopus line 1650: target += tot_boost-(19<<LM)
	//
	// tot_boost comes from DynallocAnalysis() and represents extra bits
	// needed for bands with high energy variance.
	// We use the previous frame's analysis (stored in lastDynalloc).
	totBoost := e.lastDynalloc.TotBoost
	if totBoost == 0 {
		// Fallback for first frame or when dynalloc not computed
		totBoost = 200 << bitRes // ~200 bits boost (in Q3)
	}
	calibration := 19 << lm
	targetQ3 += totBoost - calibration
	if targetQ3 < 0 {
		targetQ3 = 0
	}

	// Apply transient/tf_estimate boost
	// Reference: libopus line 1652-1653:
	// tf_calibration = QCONST16(0.044f,14);
	// target += (opus_int32)SHL32(MULT16_32_Q15(tf_estimate-tf_calibration, target),1);
	//
	// tf_estimate is in Q14 format (0.0 to 1.0 scaled by 16384)
	// For steady-state audio (non-transient), tf_estimate is typically 0.1-0.3
	// We use 0.15 as a reasonable default.
	tfEstimateQ14 := 2458   // ~0.15 in Q14
	tfCalibrationQ14 := 721 // 0.044 in Q14
	tfDiff := tfEstimateQ14 - tfCalibrationQ14
	// Boost: target *= (1 + 2 * tfDiff / 32768)
	if tfDiff > 0 {
		boost := (targetQ3 * tfDiff * 2) >> 15
		targetQ3 += boost
	}

	// Apply tonality boost (biggest contributor for tonal signals like sine waves)
	// Reference: libopus lines 1657-1669
	// tonal = MAX16(0.f,analysis->tonality-.15f)-0.12f
	// tonal_target = target + (coded_bins<<BITRES)*1.2f*tonal
	//
	// Use stored tonality from previous frame's analysis (via Spectral Flatness Measure).
	// Libopus similarly uses analysis from the previous frame for current VBR decisions.
	// The tonality value ranges from 0 (noise) to 1 (pure tone).
	// For tonal signals (sine waves, pitched instruments), values can reach 0.95+.
	tonality := e.lastTonality
	if tonality > 0.15 {
		tonal := tonality - 0.15 - 0.12 // Apply thresholds from libopus
		if tonal > 0 {
			// tonal_boost = coded_bins * BITRES * 1.2 * tonal
			tonalBoost := int(float64(codedBins<<bitRes) * 1.2 * tonal)
			targetQ3 += tonalBoost
		}
	}

	// Apply floor_depth limit (prevents over-allocation for low-level signals)
	// Reference: libopus lines 1682-1693
	//
	// maxDepth is the maximum signal level relative to noise floor (in dB),
	// computed by DynallocAnalysis(). It represents the dynamic range of the signal.
	// For very quiet signals, maxDepth will be low, limiting the bit allocation.
	//
	// The floor_depth limit only applies when maxDepth is LOW (quiet signal).
	// For normal/loud signals (maxDepth > 20 dB), we skip this clamping.
	maxDepth := e.lastDynalloc.MaxDepth
	if maxDepth > -31.0 && maxDepth != 0.0 && maxDepth < 20.0 {
		// Only apply floor_depth for quiet signals (maxDepth < 20 dB above noise)
		// This prevents over-allocating bits to signals buried in noise.
		//
		// For quiet signals, limit target to a fraction based on maxDepth.
		// At maxDepth=0, limit to target/8; at maxDepth=20, no limit.
		depthFraction := maxDepth / 20.0
		if depthFraction < 0 {
			depthFraction = 0
		}
		floorDepth := int(float64(targetQ3) * (0.125 + 0.875*depthFraction))

		// floor_depth = max(floor_depth, target/4)
		if floorDepth < targetQ3/4 {
			floorDepth = targetQ3 / 4
		}

		// target = min(target, floor_depth)
		if targetQ3 > floorDepth {
			targetQ3 = floorDepth
		}
	}

	// Limit boost to 2x base (libopus line 1713)
	// target = IMIN(2*base_target, target)
	maxTarget := 2 * baseTargetQ3
	if targetQ3 > maxTarget {
		targetQ3 = maxTarget
	}

	return targetQ3
}

// updateTonalityAnalysis computes tonality metrics from the current frame's MDCT coefficients
// and updates encoder state for use in the next frame's VBR decisions.
//
// This uses Spectral Flatness Measure (SFM) to distinguish tonal signals (music, speech)
// from noisy signals. The analysis is stored for the next frame because VBR target
// computation happens before MDCT in the encoding pipeline (matching libopus behavior).
//
// Parameters:
//   - normCoeffs: normalized MDCT coefficients (left channel for stereo)
//   - energies: band energies (log-domain) for spectral flux computation
//   - nbBands: number of frequency bands
//   - frameSize: frame size in samples (unused but kept for API consistency)
func (e *Encoder) updateTonalityAnalysis(normCoeffs, energies []float64, nbBands, frameSize int) {
	// Compute tonality using Spectral Flatness Measure
	// ComputeTonality takes coeffs and optional previous coeffs for flux calculation
	tonalityResult := ComputeTonalityWithBands(normCoeffs, nbBands, frameSize)

	// Compute spectral flux (frame-to-frame change) for smoothing decisions
	spectralFlux := ComputeSpectralFlux(energies, e.prevBandLogEnergy, nbBands)

	// Update previous band log-energies for next frame's flux computation
	copy(e.prevBandLogEnergy, energies)

	// Apply smoothing to tonality estimate
	// High spectral flux (transients) should reduce the smoothing factor
	// to allow faster adaptation to signal changes.
	// Low flux (stationary signals) uses more smoothing for stability.
	//
	// Smoothing formula: tonality = alpha * new + (1-alpha) * old
	// where alpha = 0.3 + 0.4 * spectralFlux (range: 0.3 to 0.7)
	alpha := 0.3 + 0.4*spectralFlux
	if alpha > 0.7 {
		alpha = 0.7
	}

	// Update tonality with smoothing
	e.lastTonality = alpha*tonalityResult.Tonality + (1-alpha)*e.lastTonality

	// Clamp to valid range
	if e.lastTonality < 0 {
		e.lastTonality = 0
	}
	if e.lastTonality > 1 {
		e.lastTonality = 1
	}
}
