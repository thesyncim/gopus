// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the complete frame encoding pipeline.

package celt

import (
	"errors"

	"github.com/thesyncim/gopus/rangecoding"
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

	// Ensure scratch buffers are properly sized for this frame
	e.ensureScratch(frameSize)

	// Step 3a: Apply DC rejection (high-pass) to match libopus Opus encoder.
	// libopus applies dc_reject() at the Opus encoder level before CELT
	// mode selection. Since gopus invokes the CELT encoder directly for
	// CELT mode, we apply dc_reject here to match the full encode path.
	// Reference: libopus src/opus_encoder.c line 2008: dc_reject(pcm, 3, ...)
	dcRejected := e.applyDCRejectScratch(pcm)

	// Step 3b: Apply delay buffer (lookahead compensation)
	// libopus uses a delay_compensation of Fs/250 = 192 samples at 48kHz.
	// The delay buffer is prepended to the new samples, creating a lookahead.
	// Reference: libopus src/opus_encoder.c line 1967
	delayComp := DelayCompensation * e.channels
	if len(e.delayBuffer) < delayComp {
		e.delayBuffer = make([]float64, delayComp)
	}

	// Build combined buffer: [delay_buffer] + [PCM samples] using scratch
	combinedLen := delayComp + len(dcRejected)
	combinedBuf := e.scratch.combinedBuf
	if len(combinedBuf) < combinedLen {
		combinedBuf = make([]float64, combinedLen)
		e.scratch.combinedBuf = combinedBuf
	}
	combinedBuf = combinedBuf[:combinedLen]
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
	preemph := e.applyPreemphasisWithScalingScratch(samplesForFrame)

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
	// Total length: (overlap + frameSize) * channels - use scratch buffer
	transientLen := (overlap + frameSize) * e.channels
	transientInput := e.scratch.transientInput
	if len(transientInput) < transientLen {
		transientInput = make([]float64, transientLen)
		e.scratch.transientInput = transientInput
	}
	transientInput = transientInput[:transientLen]
	copy(transientInput[:preemphBufSize], e.preemphBuffer[:preemphBufSize])
	copy(transientInput[preemphBufSize:], preemph)

	// Call transient analysis with the pre-emphasized signal (N+overlap samples)
	transientResult := e.TransientAnalysis(transientInput, frameSize+overlap, false /* allowWeakTransients */)
	transient := transientResult.IsTransient
	tfEstimate := transientResult.TfEstimate
	toneFreq := transientResult.ToneFreq
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

	// For Frame 0, force transient=true to match libopus behavior.
	// libopus detects transient on first frame due to energy increase from silence.
	// Reference: libopus patch_transient_decision() and first frame handling.
	//
	// IMPORTANT: Only force transient if transient_analysis didn't already detect one.
	// If transient_analysis returned is_transient=true, USE ITS tf_estimate.
	// The tf_estimate=0.2 override in libopus (line 2230) only happens in
	// patch_transient_decision, which is only called when !isTransient.
	if e.frameCount == 0 && lm > 0 && !transient {
		transient = true
		tfEstimate = 0.2
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

	// For transients at high complexity, compute long MDCT energies (bandLogE2).
	secondMdct := shortBlocks > 1 && e.complexity >= 8
	var bandLogE2 []float64
	if secondMdct {
		if e.channels == 1 {
			overlap := Overlap
			if overlap > frameSize {
				overlap = frameSize
			}
			// Use scratch for hist buffer
			hist := e.scratch.leftHist
			if len(hist) < overlap {
				hist = make([]float64, overlap)
				e.scratch.leftHist = hist
			}
			hist = hist[:overlap]
			copy(hist, e.overlapBuffer[:overlap])
			mdctLong := computeMDCTWithHistoryScratch(preemph, hist, 1, &e.scratch)
			bandLogE2 = e.ComputeBandEnergies(mdctLong, nbBands, frameSize)
			roundFloat64ToFloat32(bandLogE2)
		} else {
			left, right := deinterleaveStereoScratch(preemph, &e.scratch.deintLeft, &e.scratch.deintRight)
			overlap := Overlap
			if overlap > frameSize {
				overlap = frameSize
			}
			if len(e.overlapBuffer) < 2*overlap {
				newBuf := make([]float64, 2*overlap)
				if len(e.overlapBuffer) > 0 {
					copy(newBuf, e.overlapBuffer)
				}
				e.overlapBuffer = newBuf
			}
			// Use scratch for hist buffers
			leftHist := e.scratch.leftHist
			rightHist := e.scratch.rightHist
			if len(leftHist) < overlap {
				leftHist = make([]float64, overlap)
				e.scratch.leftHist = leftHist
			}
			if len(rightHist) < overlap {
				rightHist = make([]float64, overlap)
				e.scratch.rightHist = rightHist
			}
			leftHist = leftHist[:overlap]
			rightHist = rightHist[:overlap]
			copy(leftHist, e.overlapBuffer[:overlap])
			copy(rightHist, e.overlapBuffer[overlap:2*overlap])
			mdctLeftLong := computeMDCTWithHistoryScratchStereoL(left, leftHist, 1, &e.scratch)
			mdctRightLong := computeMDCTWithHistoryScratchStereoR(right, rightHist, 1, &e.scratch)
			// Use scratch for combined mdct
			mdctLongLen := len(mdctLeftLong) + len(mdctRightLong)
			mdctLong := e.scratch.mdctCoeffs
			if len(mdctLong) < mdctLongLen {
				mdctLong = make([]float64, mdctLongLen)
				e.scratch.mdctCoeffs = mdctLong
			}
			mdctLong = mdctLong[:mdctLongLen]
			copy(mdctLong, mdctLeftLong)
			copy(mdctLong[len(mdctLeftLong):], mdctRightLong)
			bandLogE2 = e.ComputeBandEnergies(mdctLong, nbBands, frameSize)
			roundFloat64ToFloat32(bandLogE2)
		}
		if bandLogE2 != nil {
			offset := 0.5 * float64(lm)
			for i := range bandLogE2 {
				bandLogE2[i] += offset
			}
			// Match libopus float path precision after offset addition.
			roundFloat64ToFloat32(bandLogE2)
		}
	}

	// Step 5: Compute MDCT with proper overlap handling
	var mdctCoeffs []float64
	var mdctLeft, mdctRight []float64
	if e.channels == 1 {
		// Mono: MDCT directly with overlap buffer for continuity
		mdctCoeffs = e.computeMDCTWithOverlap(preemph, shortBlocks)
	} else {
		// Stereo: MDCT Left and Right directly - use scratch buffers
		left, right := deinterleaveStereoScratch(preemph, &e.scratch.deintLeft, &e.scratch.deintRight)

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

		// Use overlap-aware MDCT for both channels with scratch buffers
		mdctLeft = computeMDCTWithHistoryScratchStereoL(left, leftHistory, shortBlocks, &e.scratch)
		mdctRight = computeMDCTWithHistoryScratchStereoR(right, rightHistory, shortBlocks, &e.scratch)

		// Concatenate: [left coeffs][right coeffs] - use scratch buffer
		coeffsLen := len(mdctLeft) + len(mdctRight)
		mdctCoeffs = e.scratch.mdctCoeffs
		if len(mdctCoeffs) < coeffsLen {
			mdctCoeffs = make([]float64, coeffsLen)
			e.scratch.mdctCoeffs = mdctCoeffs
		}
		mdctCoeffs = mdctCoeffs[:coeffsLen]
		copy(mdctCoeffs[:len(mdctLeft)], mdctLeft)
		copy(mdctCoeffs[len(mdctLeft):], mdctRight)
	}

	// Step 6: Compute band energies
	energies := e.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	roundFloat64ToFloat32(energies)
	if !secondMdct {
		bandLogE2 = make([]float64, len(energies))
		copy(bandLogE2, energies)
	}

	// Step 6.5: Patch transient decision based on band energy comparison
	// This is a "second chance" to detect transients that time-domain analysis missed.
	// Particularly important for the first frame where buffer initialization may cause
	// false negatives in transient_analysis().
	// Reference: libopus celt/celt_encoder.c lines 2215-2231
	end := nbBands
	if lm > 0 && !transient && e.complexity >= 5 && !e.IsHybrid() {
		// Get previous frame's band energies (oldBandE in libopus)
		oldBandE := e.prevEnergy

		if PatchTransientDecision(energies, oldBandE, nbBands, 0, end, e.channels) {
			// Transient patched! Need to recompute MDCT with short blocks
			transient = true
			shortBlocks = mode.ShortBlocks
			tfEstimate = 0.2 // Match libopus: tf_estimate = QCONST16(.2f,14)

			// Recompute MDCT with short blocks
			if e.channels == 1 {
				// For mono, we need to restore overlap buffer state before recomputing
				// Since computeMDCTWithOverlap updates the buffer, we can just call it again
				// with the new shortBlocks value
				mdctCoeffs = computeMDCTForEncoding(preemph, frameSize, shortBlocks)
			} else {
				// For stereo, recompute both channels - use scratch buffers
				left, right := deinterleaveStereoScratch(preemph, &e.scratch.deintLeft, &e.scratch.deintRight)
				overlap := Overlap
				if overlap > frameSize {
					overlap = frameSize
				}
				// Use scratch history slices to avoid double-update issues
				leftHist := e.scratch.leftHist
				rightHist := e.scratch.rightHist
				if len(leftHist) < overlap {
					leftHist = make([]float64, overlap)
					e.scratch.leftHist = leftHist
				}
				if len(rightHist) < overlap {
					rightHist = make([]float64, overlap)
					e.scratch.rightHist = rightHist
				}
				leftHist = leftHist[:overlap]
				rightHist = rightHist[:overlap]
				if len(e.overlapBuffer) >= 2*overlap {
					copy(leftHist, e.overlapBuffer[:overlap])
					copy(rightHist, e.overlapBuffer[overlap:2*overlap])
				}
				mdctLeft = computeMDCTWithHistoryScratchStereoL(left, leftHist, shortBlocks, &e.scratch)
				mdctRight = computeMDCTWithHistoryScratchStereoR(right, rightHist, shortBlocks, &e.scratch)
				// Use scratch buffer for combined coefficients
				coeffsLen := len(mdctLeft) + len(mdctRight)
				mdctCoeffs = e.scratch.mdctCoeffs
				if len(mdctCoeffs) < coeffsLen {
					mdctCoeffs = make([]float64, coeffsLen)
					e.scratch.mdctCoeffs = mdctCoeffs
				}
				mdctCoeffs = mdctCoeffs[:coeffsLen]
				copy(mdctCoeffs[:len(mdctLeft)], mdctLeft)
				copy(mdctCoeffs[len(mdctLeft):], mdctRight)
			}

			// Recompute band energies with short block coefficients
			energies = e.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
			roundFloat64ToFloat32(energies)
			// Compensate for scaling of short vs long MDCTs (libopus adds 0.5*LM to bandLogE2)
			if bandLogE2 != nil {
				offset := 0.5 * float64(lm)
				for i := range bandLogE2 {
					bandLogE2[i] += offset
				}
				// Match libopus float path precision after offset addition.
				roundFloat64ToFloat32(bandLogE2)
			}
		}
	}

	// Store band log-energies for debugging/analysis.
	// These are the values passed to DynallocAnalysis.
	e.lastBandLogE = append(e.lastBandLogE[:0], energies...)
	if bandLogE2 != nil {
		e.lastBandLogE2 = append(e.lastBandLogE2[:0], bandLogE2...)
	} else {
		e.lastBandLogE2 = e.lastBandLogE2[:0]
	}

	// Compute linear band amplitudes directly from MDCT coefficients.
	// This matches libopus compute_band_energies() which returns sqrt(sum of squares).
	// CRITICAL: We must use ORIGINAL linear amplitudes, not log-domain converted back to linear,
	// to avoid quantization/roundtrip errors that corrupt PVQ encoding.
	// Reference: libopus celt_encoder.c line 2096
	var bandE []float64
	if e.channels == 1 {
		bandE = ComputeLinearBandAmplitudes(mdctCoeffs, nbBands, frameSize)
	} else {
		// For stereo, compute per-channel and concatenate - use scratch buffers
		bandEL := ComputeLinearBandAmplitudes(mdctLeft, nbBands, frameSize)
		bandER := ComputeLinearBandAmplitudes(mdctRight, nbBands, frameSize)
		bandE = e.scratch.bandE
		if len(bandE) < nbBands*2 {
			bandE = make([]float64, nbBands*2)
			e.scratch.bandE = bandE
		}
		bandE = bandE[:nbBands*2]
		copy(bandE[:nbBands], bandEL)
		copy(bandE[nbBands:], bandER)
	}

	// Step 7: Initialize range encoder with bitrate-derived size
	// ... (no changes here) ...
	targetBits := e.computeTargetBits(frameSize)
	e.frameBits = targetBits
	defer func() { e.frameBits = 0 }()
	targetBytes := (targetBits + 7) / 8
	bufSize := targetBytes
	if bufSize < 256 {
		bufSize = 256
	}
	// Use scratch buffer for range encoder
	buf := e.scratch.reBuf
	if len(buf) < bufSize {
		buf = make([]byte, bufSize)
		e.scratch.reBuf = buf
	}
	buf = buf[:bufSize]
	re := &rangecoding.Encoder{}
	re.Init(buf)
	// In CBR mode, shrink the encoder to produce exactly targetBytes output.
	// This matches libopus ec_enc_shrink() behavior for constant bitrate encoding.
	// Reference: libopus celt_encoder.c line 1920: ec_enc_shrink(enc, nbCompressedBytes)
	if !e.vbr {
		re.Shrink(uint32(targetBytes))
	}
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
	// Use scratch buffer for prev1LogE
	prev1LogE := e.scratch.prev1LogE
	if len(prev1LogE) < len(e.prevEnergy) {
		prev1LogE = make([]float64, len(e.prevEnergy))
		e.scratch.prev1LogE = prev1LogE
	}
	prev1LogE = prev1LogE[:len(e.prevEnergy)]
	copy(prev1LogE, e.prevEnergy)
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
	// Note: 'end' was already set earlier during patch_transient_decision
	effectiveBytes := 0
	if e.vbr {
		baseBits := e.bitrateToBits(frameSize)
		effectiveBytes = baseBits / 8
	} else {
		effectiveBytes = e.cbrPayloadBytes(frameSize)
	}

	// Step 11.0.7: Compute dynalloc analysis for VBR and bit allocation
	// This computes maxDepth, offsets, importance, and spread_weight.
	// The results are stored for next frame's VBR target computation.
	// Reference: libopus celt/celt_encoder.c dynalloc_analysis()
	//
	// libopus defaults to 24 for float input (see celt_encoder.c: st->lsb_depth=24).
	// Our encoder operates on float64 samples, so match the float path.
	lsbDepth := 24
	// Use scratch buffer for logN
	logN := e.scratch.logN
	if len(logN) < nbBands {
		logN = make([]int16, nbBands)
		e.scratch.logN = logN
	}
	logN = logN[:nbBands]
	for i := 0; i < nbBands && i < len(LogN); i++ {
		logN[i] = int16(LogN[i])
	}
	// Determine VBR mode (match encoder settings)
	isVBR := e.vbr
	isConstrainedVBR := e.constrainedVBR
	bandLogE2Use := energies
	if bandLogE2 != nil {
		bandLogE2Use = bandLogE2
	}
	dynallocResult := DynallocAnalysisWithScratch(
		energies, bandLogE2Use, prev1LogE,
		nbBands, start, end, e.channels, lsbDepth, lm,
		logN,
		effectiveBytes,
		transient, isVBR, isConstrainedVBR,
		toneFreq, toneishness,
		&e.dynallocScratch,
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

		// Use the normalized coefficients for TF analysis (zero-alloc version)
		// For stereo, use the left channel (similar to libopus tf_chan approach)
		tfRes, tfSelect = TFAnalysisWithScratch(normL, len(normL), nbBands, transient, lm, useTfEstimate, effectiveBytes, importance, &e.tfScratch)

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
	// Reference: libopus celt_encoder.c line 2302-2345
	var spread int
	if re.Tell()+4 <= targetBits {
		// For transient frames (shortBlocks), low complexity, or very low bitrate,
		// skip spreading_decision() analysis and use simple defaults.
		// Reference: libopus celt_encoder.c line 2316-2321
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
			// For non-transient frames with sufficient bits, analyze the signal
			// to determine optimal spreading.
			// Reference: libopus celt_encoder.c spreading_decision() call with
			// pf_on&&!shortBlocks as updateHF condition.
			updateHF := shortBlocks == 1
			// Compute dynamic spread weights based on masking analysis (matches libopus dynalloc_analysis)
			// Use lsbDepth derived above to match libopus float input.
			spreadWeights := ComputeSpreadWeights(energies, nbBands, e.channels, lsbDepth)
			spread = e.SpreadingDecisionWithWeights(normL, nbBands, e.channels, frameSize, updateHF, spreadWeights)
		}
		re.EncodeICDF(spread, spreadICDF, 5)
	} else {
		spread = spreadNormal
	}

	// Step 11.3: Initialize caps for allocation (zero-alloc)
	caps := ensureIntSlice(&e.scratch.caps, nbBands)
	initCapsInto(caps, nbBands, lm, e.channels)

	// Step 11.4: Encode dynamic allocation
	// Reference: libopus celt/celt_encoder.c lines 2356-2389
	//
	// For each band, we encode a series of bits indicating boost allocation:
	// - First bit uses logp=6 (or current dynallocLogp)
	// - Subsequent bits use logp=1 (very likely to continue boosting)
	// - A 0-bit terminates the boost for that band
	// - If offsets[i] == 0, we just encode one 0-bit and move on
	//
	// The offsets come from dynalloc_analysis() and represent how many
	// "quanta" of boost bits to allocate to each band.
	offsets := dynallocResult.Offsets
	if offsets == nil || len(offsets) < nbBands {
		offsets = make([]int, nbBands)
	}
	dynallocLogp := 6
	totalBitsQ3ForDynalloc := targetBits << bitRes
	totalBoost := 0
	tellFracDynalloc := re.TellFrac()
	for i := start; i < end; i++ {
		// Compute band width and quanta (how many bits per boost step)
		// Reference: libopus lines 2366-2369
		// width = C*(eBands[i+1]-eBands[i])<<LM
		width := e.channels * ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		// quanta = min(width<<BITRES, max(6<<BITRES, width))
		// This means quanta is between 6 bits and width bits, scaled by BITRES
		innerMax := 6 << bitRes
		if width > innerMax {
			innerMax = width
		}
		quanta := width << bitRes
		if quanta > innerMax {
			quanta = innerMax
		}

		dynallocLoopLogp := dynallocLogp
		boost := 0

		// Loop encoding boost bits while j < offsets[i]
		for j := 0; tellFracDynalloc+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoost && boost < caps[i]; j++ {
			flag := 0
			if j < offsets[i] {
				flag = 1
			}
			re.EncodeBit(flag, uint(dynallocLoopLogp))
			tellFracDynalloc = re.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoost += quanta
			dynallocLoopLogp = 1 // After first bit, use logp=1
		}

		// Making dynalloc more likely for subsequent bands if we boosted this one
		if boost > 0 {
			if dynallocLogp > 2 {
				dynallocLogp--
			}
		}

		// Update offsets[i] to reflect actual boost applied (for allocation)
		offsets[i] = boost
	}
	// Step 11.5: Compute and encode allocation trim (only if budget allows)
	// Reference: libopus celt_encoder.c line 2408-2417
	// The trim value affects bit allocation bias between lower and higher frequency bands.
	allocTrim := 5
	tellForTrim := re.TellFrac()
	if tellForTrim+(6<<bitRes) <= totalBitsQ3ForDynalloc-totalBoost {
		effectiveBytesForTrim := targetBits / 8
		equivRate := ComputeEquivRate(effectiveBytesForTrim, e.channels, lm, e.targetBitrate)
		allocTrim = AllocTrimAnalysis(
			normL,
			energies,
			nbBands,
			lm,
			e.channels,
			normR,
			intensity,
			tfEstimate,
			equivRate,
			0.0, // surroundTrim - not implemented yet
			0.0, // tonalitySlope - not implemented yet
		)

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
		totalBitsQ3,
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
	quantAllBandsEncodeScratch(
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
		&e.bandEncScratch,
	)

	// Step 14.5: Encode anti-collapse flag if reserved
	if antiCollapseRsv > 0 {
		antiCollapseOn := 0
		if e.consecTransient < 2 {
			antiCollapseOn = 1
		}
		re.EncodeRawBits(uint32(antiCollapseOn), 1)
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
	if transient {
		e.consecTransient++
	} else {
		e.consecTransient = 0
	}

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

	return computeMDCTWithHistoryScratch(samples, e.overlapBuffer[:overlap], shortBlocks, &e.scratch)
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

// computeMDCTWithHistoryScratch computes MDCT using a history buffer with scratch buffers.
// This is the zero-allocation version that uses pre-allocated buffers.
func computeMDCTWithHistoryScratch(samples, history []float64, shortBlocks int, scratch *encoderScratch) []float64 {
	if len(samples) == 0 {
		return nil
	}

	overlap := Overlap
	if overlap > len(samples) {
		overlap = len(samples)
	}

	// Use scratch input buffer
	inputLen := len(samples) + overlap
	input := scratch.mdctInput
	if len(input) < inputLen {
		input = make([]float64, inputLen)
		scratch.mdctInput = input
	}
	input = input[:inputLen]

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
		return mdctShortScratch(input, shortBlocks, scratch)
	}
	return mdctScratch(input, scratch)
}

// computeMDCTWithHistoryScratchStereoL computes MDCT for the left channel with scratch buffers.
// Uses mdctLeft scratch buffer for output.
func computeMDCTWithHistoryScratchStereoL(samples, history []float64, shortBlocks int, scratch *encoderScratch) []float64 {
	if len(samples) == 0 {
		return nil
	}

	overlap := Overlap
	if overlap > len(samples) {
		overlap = len(samples)
	}

	// Use scratch input buffer (shared, but reused between L and R sequentially)
	inputLen := len(samples) + overlap
	input := scratch.mdctInput
	if len(input) < inputLen {
		input = make([]float64, inputLen)
		scratch.mdctInput = input
	}
	input = input[:inputLen]

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

	// Use mdctLeft for output
	frameSize := len(samples)
	coeffs := ensureFloat64Slice(&scratch.mdctLeft, frameSize)

	if shortBlocks > 1 {
		return mdctShortScratchInto(input, shortBlocks, coeffs, scratch)
	}
	return mdctScratchInto(input, coeffs, scratch)
}

// computeMDCTWithHistoryScratchStereoR computes MDCT for the right channel with scratch buffers.
// Uses mdctRight scratch buffer for output.
func computeMDCTWithHistoryScratchStereoR(samples, history []float64, shortBlocks int, scratch *encoderScratch) []float64 {
	if len(samples) == 0 {
		return nil
	}

	overlap := Overlap
	if overlap > len(samples) {
		overlap = len(samples)
	}

	// Use scratch input buffer (shared, but reused between L and R sequentially)
	inputLen := len(samples) + overlap
	input := scratch.mdctInput
	if len(input) < inputLen {
		input = make([]float64, inputLen)
		scratch.mdctInput = input
	}
	input = input[:inputLen]

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

	// Use mdctRight for output
	frameSize := len(samples)
	coeffs := ensureFloat64Slice(&scratch.mdctRight, frameSize)

	if shortBlocks > 1 {
		return mdctShortScratchInto(input, shortBlocks, coeffs, scratch)
	}
	return mdctScratchInto(input, coeffs, scratch)
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

// bitrateToBits returns the base target bits from bitrate and frame size.
// This mirrors libopus bitrate_to_bits() for CELT frames.
func (e *Encoder) bitrateToBits(frameSize int) int {
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
	return bitrate * frameSize / 48000
}

// cbrPayloadBytes computes the CBR payload size (excluding TOC).
// This matches libopus's CBR byte formula and subtracts the TOC byte.
func (e *Encoder) cbrPayloadBytes(frameSize int) int {
	const fs = 48000
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
	nbCompressed := (bitrate*frameSize + 4*fs) / (8 * fs)
	if nbCompressed < 2 {
		nbCompressed = 2
	}
	if nbCompressed > 1275 {
		nbCompressed = 1275
	}
	payload := nbCompressed - 1 // subtract TOC byte
	if payload < 0 {
		payload = 0
	}
	return payload
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
	// CBR path uses fixed payload size.
	if !e.vbr {
		return e.cbrPayloadBytes(frameSize) * 8
	}

	baseBits := e.bitrateToBits(frameSize)

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
	targetQ3 := e.computeVBRTarget(baseTargetQ3, frameSize)

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
func (e *Encoder) computeVBRTarget(baseTargetQ3, frameSize int) int {
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
	// Compute tonality using Spectral Flatness Measure (zero-alloc version)
	tonalityResult := ComputeTonalityWithBandsScratch(normCoeffs, nbBands, frameSize, &e.tonalityScratch)

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
