package celt

import "github.com/thesyncim/gopus/rangecoding"

// NormalizeBandsToArrayMonoWithBandE normalizes MDCT coefficients for mono
// and returns the normalized coefficients and linear band amplitudes.
func (e *Encoder) NormalizeBandsToArrayMonoWithBandE(mdctCoeffs []float64, nbBands, frameSize int) (norm []float64, bandE []float64) {
	norm = ensureFloat64Slice(&e.scratch.normL, frameSize)
	bandE = ensureFloat64Slice(&e.scratch.bandE, nbBands)
	NormalizeBandsToArrayInto(mdctCoeffs, nbBands, frameSize, norm, bandE)
	return norm, bandE
}

// NormalizeBandsToArrayStereoWithBandE normalizes MDCT coefficients for stereo
// and returns normalized L/R coefficients plus combined linear band amplitudes.
// The bandE layout is [L bands][R bands].
func (e *Encoder) NormalizeBandsToArrayStereoWithBandE(mdctLeft, mdctRight []float64, nbBands, frameSize int) (normL, normR, bandE []float64) {
	normL = ensureFloat64Slice(&e.scratch.normL, frameSize)
	normR = ensureFloat64Slice(&e.scratch.normR, frameSize)
	bandEL := ensureFloat64Slice(&e.scratch.bandEL, nbBands)
	bandER := ensureFloat64Slice(&e.scratch.bandER, nbBands)
	NormalizeBandsToArrayInto(mdctLeft, nbBands, frameSize, normL, bandEL)
	NormalizeBandsToArrayInto(mdctRight, nbBands, frameSize, normR, bandER)
	bandE = ensureFloat64Slice(&e.scratch.bandE, nbBands*2)
	copy(bandE[:nbBands], bandEL)
	copy(bandE[nbBands:], bandER)
	return normL, normR, bandE
}

// TFResScratch returns a scratch TF resolution slice sized for nbBands.
func (e *Encoder) TFResScratch(nbBands int) []int {
	return ensureIntSlice(&e.scratch.tfRes, nbBands)
}

// CapsScratch returns a scratch caps slice sized for nbBands.
func (e *Encoder) CapsScratch(nbBands int) []int {
	return ensureIntSlice(&e.scratch.caps, nbBands)
}

// OffsetsScratch returns a scratch offsets slice sized for nbBands.
func (e *Encoder) OffsetsScratch(nbBands int) []int {
	return ensureIntSlice(&e.scratch.offsets, nbBands)
}

// ComputeAllocationHybridScratch computes hybrid bit allocation using encoder scratch.
// This mirrors ComputeAllocationHybrid but avoids per-call allocations.
func (e *Encoder) ComputeAllocationHybridScratch(re *rangecoding.Encoder, totalBitsQ3, nbBands int, cap, offsets []int, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) *AllocationResult {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	channels := e.channels
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	result := &e.scratch.allocResult
	result.BandBits = ensureIntSlice(&e.scratch.allocBits, nbBands)
	result.FineBits = ensureIntSlice(&e.scratch.allocFineBits, nbBands)
	result.FinePriority = ensureIntSlice(&e.scratch.allocFinePrio, nbBands)
	result.Caps = ensureIntSlice(&e.scratch.allocCaps, nbBands)
	result.Balance = 0
	result.CodedBands = nbBands
	result.Intensity = 0
	result.DualStereo = false

	for i := 0; i < nbBands; i++ {
		result.BandBits[i] = 0
		result.FineBits[i] = 0
		result.FinePriority[i] = 0
		result.Caps[i] = 0
	}

	if nbBands == 0 || totalBitsQ3 <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		cap = initCaps(nbBands, lm, channels)
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = e.scratch.offsets[:nbBands]
		for i := range offsets {
			offsets[i] = 0
		}
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	codedBands := cltComputeAllocationEncode(re, HybridCELTStartBand, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
		totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth)

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

// QuantAllBandsEncodeScratch encodes PVQ bands using the encoder's scratch buffers.
func (e *Encoder) QuantAllBandsEncodeScratch(re *rangecoding.Encoder, channels, frameSize, lm int, start, end int,
	normL, normR []float64, pulses []int, shortBlocks int, spread int, tapset int, dualStereo int, intensity int,
	tfRes []int, totalBitsQ3 int, balance int, codedBands int, seed *uint32, complexity int, bandE []float64) {
	quantAllBandsEncodeScratch(
		re,
		channels,
		frameSize,
		lm,
		start,
		end,
		normL,
		normR,
		pulses,
		shortBlocks,
		spread,
		tapset,
		dualStereo,
		intensity,
		tfRes,
		totalBitsQ3,
		balance,
		codedBands,
		e.phaseInversionDisabled,
		seed,
		complexity,
		bandE,
		nil,
		nil,
		&e.bandEncScratch,
	)
}

// LastCodedBands returns the last coded band count used for allocation skip decisions.
func (e *Encoder) LastCodedBands() int {
	return e.lastCodedBands
}

// SetLastCodedBands updates the last coded band count.
func (e *Encoder) SetLastCodedBands(val int) {
	e.lastCodedBands = val
}

// ConsecTransient returns the number of consecutive transient frames.
func (e *Encoder) ConsecTransient() int {
	return e.consecTransient
}

// UpdateConsecTransient updates the consecutive transient counter.
func (e *Encoder) UpdateConsecTransient(transient bool) {
	if transient {
		e.consecTransient++
	} else {
		e.consecTransient = 0
	}
}

// TransientAnalysisHybrid performs transient analysis and updates preemph overlap state.
// Returns transient flags, tf/tone metrics, shortBlocks choice, and optional bandLogE2.
func (e *Encoder) TransientAnalysisHybrid(preemph []float64, frameSize, nbBands, lm int) (transient bool, tfEstimate, toneFreq, toneishness float64, shortBlocks int, bandLogE2 []float64) {
	overlap := Overlap
	if overlap > frameSize {
		overlap = frameSize
	}

	preemphBufSize := overlap * e.channels
	if len(e.preemphBuffer) < preemphBufSize {
		e.preemphBuffer = make([]float64, preemphBufSize)
	}

	transientLen := (overlap + frameSize) * e.channels
	transientInput := e.scratch.transientInput
	if len(transientInput) < transientLen {
		transientInput = make([]float64, transientLen)
		e.scratch.transientInput = transientInput
	}
	transientInput = transientInput[:transientLen]
	copy(transientInput[:preemphBufSize], e.preemphBuffer[:preemphBufSize])
	copy(transientInput[preemphBufSize:], preemph)

	result := e.TransientAnalysis(transientInput, frameSize+overlap, false)
	transient = result.IsTransient
	tfEstimate = result.TfEstimate
	toneFreq = result.ToneFreq
	toneishness = result.Toneishness

	maxToneishness := 1.0 - tfEstimate
	if toneishness > maxToneishness {
		toneishness = maxToneishness
	}

	if e.forceTransient {
		transient = true
	}
	if e.frameCount == 0 && lm > 0 && !transient {
		transient = true
		tfEstimate = 0.2
	}

	shortBlocks = 1
	if transient {
		mode := GetModeConfig(frameSize)
		shortBlocks = mode.ShortBlocks
	}

	tailStart := len(preemph) - preemphBufSize
	if tailStart >= 0 {
		copy(e.preemphBuffer[:preemphBufSize], preemph[tailStart:])
	}

	secondMdct := shortBlocks > 1 && e.complexity >= 8
	if !secondMdct {
		return transient, tfEstimate, toneFreq, toneishness, shortBlocks, nil
	}

	if e.channels == 1 {
		hist := e.scratch.leftHist
		if len(hist) < overlap {
			hist = make([]float64, overlap)
			e.scratch.leftHist = hist
		}
		hist = hist[:overlap]
		copy(hist, e.overlapBuffer[:overlap])
		mdctLong := computeMDCTWithHistoryScratch(preemph, hist, 1, &e.scratch)
		bandLogE2 = ensureFloat64Slice(&e.scratch.bandLogE2, nbBands*e.channels)
		e.ComputeBandEnergiesInto(mdctLong, nbBands, frameSize, bandLogE2)
		roundFloat64ToFloat32(bandLogE2)
	} else {
		left, right := deinterleaveStereoScratch(preemph, &e.scratch.deintLeft, &e.scratch.deintRight)
		if len(e.overlapBuffer) < 2*overlap {
			newBuf := make([]float64, 2*overlap)
			if len(e.overlapBuffer) > 0 {
				copy(newBuf, e.overlapBuffer)
			}
			e.overlapBuffer = newBuf
		}
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
		mdctLongLen := len(mdctLeftLong) + len(mdctRightLong)
		mdctLong := e.scratch.mdctCoeffs
		if len(mdctLong) < mdctLongLen {
			mdctLong = make([]float64, mdctLongLen)
			e.scratch.mdctCoeffs = mdctLong
		}
		mdctLong = mdctLong[:mdctLongLen]
		copy(mdctLong, mdctLeftLong)
		copy(mdctLong[len(mdctLeftLong):], mdctRightLong)
		bandLogE2 = ensureFloat64Slice(&e.scratch.bandLogE2, nbBands*e.channels)
		e.ComputeBandEnergiesInto(mdctLong, nbBands, frameSize, bandLogE2)
		roundFloat64ToFloat32(bandLogE2)
	}

	if bandLogE2 != nil {
		offset := 0.5 * float64(lm)
		for i := range bandLogE2 {
			bandLogE2[i] += offset
		}
		roundFloat64ToFloat32(bandLogE2)
	}

	return transient, tfEstimate, toneFreq, toneishness, shortBlocks, bandLogE2
}

// DynallocAnalysisHybridScratch runs dynalloc analysis using encoder scratch buffers.
func (e *Encoder) DynallocAnalysisHybridScratch(bandLogE, bandLogE2, oldBandE []float64, nbBands, start, end, lsbDepth, lm int, effectiveBytes int, isTransient, vbr, constrainedVBR bool, toneFreq, toneishness float64) DynallocResult {
	if nbBands < 0 {
		nbBands = 0
	}
	if start < 0 {
		start = 0
	}
	if end > nbBands {
		end = nbBands
	}
	if end < start {
		end = start
	}

	logN := e.scratch.logN
	if len(logN) < nbBands {
		logN = make([]int16, nbBands)
		e.scratch.logN = logN
	}
	logN = logN[:nbBands]
	for i := 0; i < nbBands && i < len(LogN); i++ {
		logN[i] = int16(LogN[i])
	}

	result := DynallocAnalysisWithScratch(
		bandLogE,
		bandLogE2,
		oldBandE,
		nbBands,
		start,
		end,
		e.channels,
		lsbDepth,
		lm,
		logN,
		effectiveBytes,
		isTransient,
		vbr,
		constrainedVBR,
		toneFreq,
		toneishness,
		&e.dynallocScratch,
	)
	e.lastDynalloc = result
	return result
}

// TFAnalysisHybridScratch runs TF analysis using the encoder's scratch buffers.
func (e *Encoder) TFAnalysisHybridScratch(norm []float64, nbBands int, transient bool, lm int, tfEstimate float64, effectiveBytes int, importance []int) ([]int, int) {
	return TFAnalysisWithScratch(norm, len(norm), nbBands, transient, lm, tfEstimate, effectiveBytes, importance, &e.tfScratch)
}

// UpdateTonalityAnalysisHybrid updates tonality metrics for VBR decisions.
func (e *Encoder) UpdateTonalityAnalysisHybrid(normCoeffs, energies []float64, nbBands, frameSize int) {
	e.updateTonalityAnalysis(normCoeffs, energies, nbBands, frameSize)
}

// BitrateToBits exposes bitrate_to_bits for hybrid callers.
func (e *Encoder) BitrateToBits(frameSize int) int {
	return e.bitrateToBits(frameSize)
}

// CBRPayloadBytes exposes cbrPayloadBytes for hybrid callers.
func (e *Encoder) CBRPayloadBytes(frameSize int) int {
	return e.cbrPayloadBytes(frameSize)
}

// RoundFloat64ToFloat32 rounds each element to float32 precision and back.
func (e *Encoder) RoundFloat64ToFloat32(x []float64) {
	roundFloat64ToFloat32(x)
}

// MDCTScratch computes the MDCT using the encoder's pre-allocated scratch buffers.
// This is the zero-allocation equivalent of the public MDCT function.
// EnsureScratch must have been called with an appropriate frameSize first.
func (e *Encoder) MDCTScratch(samples []float64) []float64 {
	return mdctScratch(samples, &e.scratch)
}

// MDCTShortScratch computes the short-block MDCT using scratch buffers.
// This is the zero-allocation equivalent of MDCTShort.
// EnsureScratch must have been called with an appropriate frameSize first.
func (e *Encoder) MDCTShortScratch(samples []float64, shortBlocks int) []float64 {
	return mdctShortScratch(samples, shortBlocks, &e.scratch)
}

// ComputeMDCTWithHistoryScratch computes MDCT with history using scratch buffers.
// inputScratch is used to assemble [history|samples] before the transform.
// history is updated in-place with the current frame's tail.
// EnsureScratch must have been called first.
func (e *Encoder) ComputeMDCTWithHistoryScratch(inputScratch, samples, history []float64, shortBlocks int) []float64 {
	if len(samples) == 0 {
		return nil
	}

	overlap := Overlap
	if overlap > len(samples) {
		overlap = len(samples)
	}
	input := inputScratch[:len(samples)+overlap]

	// Copy history overlap into the head of the input buffer.
	if overlap > 0 && len(history) > 0 {
		if len(history) >= overlap {
			copy(input[:overlap], history[len(history)-overlap:])
		} else {
			start := overlap - len(history)
			for i := 0; i < start; i++ {
				input[i] = 0
			}
			copy(input[start:overlap], history)
		}
	} else {
		for i := 0; i < overlap; i++ {
			input[i] = 0
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
		return mdctShortScratch(input, shortBlocks, &e.scratch)
	}
	return mdctScratch(input, &e.scratch)
}

// ComputeMDCTWithHistoryScratchStereoL computes MDCT for the left channel using
// separate scratch output buffers. The result is written to scratch.mdctLeft so it
// survives a subsequent right-channel call.
func (e *Encoder) ComputeMDCTWithHistoryScratchStereoL(samples, history []float64, shortBlocks int) []float64 {
	return computeMDCTWithHistoryScratchStereoL(samples, history, shortBlocks, &e.scratch)
}

// ComputeMDCTWithHistoryScratchStereoR computes MDCT for the right channel using
// separate scratch output buffers. The result is written to scratch.mdctRight.
func (e *Encoder) ComputeMDCTWithHistoryScratchStereoR(samples, history []float64, shortBlocks int) []float64 {
	return computeMDCTWithHistoryScratchStereoR(samples, history, shortBlocks, &e.scratch)
}

// ApplyDCRejectScratchHybrid applies DC rejection using the encoder scratch buffers.
func (e *Encoder) ApplyDCRejectScratchHybrid(pcm []float64) []float64 {
	return e.applyDCRejectScratch(pcm)
}

// ApplyDelayCompensationScratchHybrid applies CELT delay compensation using encoder state.
// It prepends the delay buffer and returns a frame-sized slice of samples.
func (e *Encoder) ApplyDelayCompensationScratchHybrid(pcm []float64, frameSize int) []float64 {
	expectedLen := frameSize * e.channels
	delayComp := DelayCompensation * e.channels
	if len(e.delayBuffer) < delayComp {
		e.delayBuffer = make([]float64, delayComp)
	}

	combinedLen := delayComp + len(pcm)
	combinedBuf := e.scratch.combinedBuf
	if len(combinedBuf) < combinedLen {
		combinedBuf = make([]float64, combinedLen)
		e.scratch.combinedBuf = combinedBuf
	}
	combinedBuf = combinedBuf[:combinedLen]
	copy(combinedBuf[:delayComp], e.delayBuffer)
	copy(combinedBuf[delayComp:], pcm)

	samplesForFrame := combinedBuf[:expectedLen]
	delayTailStart := len(combinedBuf) - delayComp
	copy(e.delayBuffer, combinedBuf[delayTailStart:])

	return samplesForFrame
}
