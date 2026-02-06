package silk

import "math"

// quantizeLSF quantizes LSF coefficients using libopus-aligned MSVQ.
// Returns stage1 index, stage2 residuals (as NLSFIndices[1:order+1]), and interpolation index.
// Per RFC 6716 Section 4.2.7.5 and libopus silk/process_NLSFs.c.
func (e *Encoder) quantizeLSF(lsfQ15 []int16, bandwidth Bandwidth, signalType int, speechActivityQ8 int, numSubframes int, interpOverride int) (int, []int, int) {
	isWideband := bandwidth == BandwidthWideband

	// Select codebook based on bandwidth
	var cb *nlsfCB
	if isWideband {
		cb = &silk_NLSF_CB_WB
	} else {
		cb = &silk_NLSF_CB_NB_MB
	}

	order := cb.order
	if len(lsfQ15) < order {
		residuals := ensureIntSlice(&e.scratchLsfResiduals, order)
		for i := range residuals {
			residuals[i] = 0
		}
		return 0, residuals, 4
	}

	// Stabilize NLSFs before quantization
	silkNLSFStabilize(lsfQ15[:order], cb.deltaMinQ15, order)

	// Compute interpolation index (blend with previous frame)
	interpIdx := interpOverride
	if interpIdx < 0 {
		interpIdx = e.computeInterpolationIndex(lsfQ15, order)
	}
	// Only force no interpolation on first frame (can't interpolate without previous NLSF).
	// Per libopus process_NLSFs.c: doInterpolate = (useInterpolatedNLSFs == 1) && (NLSFInterpCoef_Q2 < 4)
	// The 10ms frame restriction was incorrect - libopus allows interpolation for all frame sizes.
	if interpIdx < 4 && !e.haveEncoded {
		interpIdx = 4
	}

	// Compute NLSF weights (Laroia)
	wQ2 := ensureInt16Slice(&e.scratchNLSFWeights, order)
	silkNLSFWeightsLaroia(wQ2, lsfQ15, order)

	// Update weights if interpolation is used
	if interpIdx < 4 && e.haveEncoded {
		nlsf0 := ensureInt16Slice(&e.scratchNLSFTempQ15, order)
		for i := 0; i < order; i++ {
			diff := int32(lsfQ15[i]) - int32(e.prevLSFQ15[i])
			nlsf0[i] = int16(int32(e.prevLSFQ15[i]) + (int32(interpIdx)*diff >> 2))
		}
		w0Q2 := ensureInt16Slice(&e.scratchNLSFWeightsTmp, order)
		silkNLSFWeightsLaroia(w0Q2, nlsf0, order)

		iSqrQ15 := int32(silkLSHIFT(silkSMULBB(int32(interpIdx), int32(interpIdx)), 11))
		for i := 0; i < order; i++ {
			adj := silkRSHIFT(silkSMULBB(int32(w0Q2[i]), iSqrQ15), 16)
			wQ2[i] = int16((int32(wQ2[i]) >> 1) + adj)
		}
	}

	muQ20 := computeNLSFMuQ20(speechActivityQ8, numSubframes)
	nSurvivors := e.nlsfSurvivors
	if nSurvivors > cb.nVectors {
		nSurvivors = cb.nVectors
	}
	if nSurvivors < 2 {
		nSurvivors = 2
	}

	stage1Idx, residuals := e.nlsfEncode(lsfQ15, cb, wQ2, muQ20, nSurvivors, signalType)

	return stage1Idx, residuals, interpIdx
}

// quantizeLSFWithInterp quantizes LSF coefficients using a provided interpolation index.
// Returns stage1 index, stage2 residuals, and the effective interpolation index.
func (e *Encoder) quantizeLSFWithInterp(lsfQ15 []int16, bandwidth Bandwidth, signalType int, speechActivityQ8 int, numSubframes int, interpIdx int) (int, []int, int) {
	isWideband := bandwidth == BandwidthWideband

	// Select codebook based on bandwidth
	var cb *nlsfCB
	if isWideband {
		cb = &silk_NLSF_CB_WB
	} else {
		cb = &silk_NLSF_CB_NB_MB
	}

	order := cb.order
	if len(lsfQ15) < order {
		residuals := ensureIntSlice(&e.scratchLsfResiduals, order)
		for i := range residuals {
			residuals[i] = 0
		}
		return 0, residuals, 4
	}

	if !e.haveEncoded {
		interpIdx = 4
	}
	if interpIdx < 0 {
		interpIdx = 0
	}
	if interpIdx > 4 {
		interpIdx = 4
	}

	// Stabilize NLSFs before quantization
	silkNLSFStabilize(lsfQ15[:order], cb.deltaMinQ15, order)

	// Compute NLSF weights (Laroia)
	wQ2 := ensureInt16Slice(&e.scratchNLSFWeights, order)
	silkNLSFWeightsLaroia(wQ2, lsfQ15, order)

	// Update weights if interpolation is used
	if interpIdx < 4 && e.haveEncoded {
		nlsf0 := ensureInt16Slice(&e.scratchNLSFTempQ15, order)
		for i := 0; i < order; i++ {
			diff := int32(lsfQ15[i]) - int32(e.prevLSFQ15[i])
			nlsf0[i] = int16(int32(e.prevLSFQ15[i]) + (int32(interpIdx)*diff >> 2))
		}
		w0Q2 := ensureInt16Slice(&e.scratchNLSFWeightsTmp, order)
		silkNLSFWeightsLaroia(w0Q2, nlsf0, order)

		iSqrQ15 := int32(silkLSHIFT(silkSMULBB(int32(interpIdx), int32(interpIdx)), 11))
		for i := 0; i < order; i++ {
			adj := silkRSHIFT(silkSMULBB(int32(w0Q2[i]), iSqrQ15), 16)
			wQ2[i] = int16((int32(wQ2[i]) >> 1) + adj)
		}
	}

	muQ20 := computeNLSFMuQ20(speechActivityQ8, numSubframes)
	nSurvivors := e.nlsfSurvivors
	if nSurvivors > cb.nVectors {
		nSurvivors = cb.nVectors
	}
	if nSurvivors < 2 {
		nSurvivors = 2
	}

	stage1Idx, residuals := e.nlsfEncode(lsfQ15, cb, wQ2, muQ20, nSurvivors, signalType)

	return stage1Idx, residuals, interpIdx
}

// computeSymbolRate8 estimates bit cost from uint8 ICDF probabilities.
func (e *Encoder) computeSymbolRate8(symbol int, icdf []uint8) int {
	// Invalid symbol check
	if symbol < 0 {
		return 256 // Max cost for invalid symbols
	}

	// Find end of ICDF (terminated by 0)
	icdfLen := 0
	for i := 0; i < len(icdf); i++ {
		if icdf[i] == 0 {
			icdfLen = i + 1
			break
		}
	}
	if icdfLen == 0 || symbol >= icdfLen-1 {
		return 256 // Max cost for invalid symbols
	}

	// Probability = icdf[symbol] - icdf[symbol+1]
	var prob int
	if symbol == 0 {
		prob = 256 - int(icdf[0])
	} else {
		prob = int(icdf[symbol-1]) - int(icdf[symbol])
	}

	if prob <= 0 {
		return 256
	}

	// Approximate -log2(prob/256) * 8 (in 1/8 bits)
	rate := 64 - int(math.Log2(float64(prob))*8)
	if rate < 0 {
		rate = 0
	}
	return rate
}

// computeInterpolationIndex determines blend with previous frame LSF.
// Per RFC 6716 Section 4.2.7.5.3.
func (e *Encoder) computeInterpolationIndex(lsfQ15 []int16, order int) int {
	// Compare current LSF with previous frame
	if !e.haveEncoded {
		return 4 // No interpolation for first frame
	}

	var diff int64
	for i := 0; i < order && i < len(e.prevLSFQ15); i++ {
		d := int64(lsfQ15[i]) - int64(e.prevLSFQ15[i])
		diff += d * d
	}

	// Thresholds for interpolation levels
	rms := math.Sqrt(float64(diff) / float64(order))

	// More interpolation (smaller index) for smoother transitions
	if rms < 500 {
		return 0 // Heavy interpolation
	} else if rms < 1000 {
		return 1
	} else if rms < 2000 {
		return 2
	} else if rms < 4000 {
		return 3
	}
	return 4 // No interpolation
}

// decodeQuantizedNLSF reconstructs the quantized NLSF (Q15) from stage1 index and residuals.
// This mirrors the decoder's silkNLSFDecode path and avoids allocations via encoder scratch buffers.
func (e *Encoder) decodeQuantizedNLSF(stage1Idx int, residuals []int, bandwidth Bandwidth) []int16 {
	// Select codebook based on bandwidth
	var cb *nlsfCB
	if bandwidth == BandwidthWideband {
		cb = &silk_NLSF_CB_WB
	} else {
		cb = &silk_NLSF_CB_NB_MB
	}

	order := cb.order
	nlsfQ15 := ensureInt16Slice(&e.scratchLSFQ15, order)

	indices := ensureInt8Slice(&e.scratchNLSFIndices, order+1)
	indices[0] = int8(stage1Idx)
	for i := 0; i < order; i++ {
		if i < len(residuals) {
			indices[i+1] = int8(residuals[i])
		} else {
			indices[i+1] = 0
		}
	}

	ecIx := ensureInt16Slice(&e.scratchEcIx, order)
	predQ8 := ensureUint8Slice(&e.scratchPredQ8, order)
	resQ10 := ensureInt16Slice(&e.scratchResQ10, order)
	silkNLSFDecodeInto(nlsfQ15, indices, cb, ecIx, predQ8, resQ10)

	return nlsfQ15
}

// buildPredCoefQ12 constructs prediction coefficients for NSQ using quantized NLSF.
// Returns the effective interpolation index (may be forced to 4 on failure).
func (e *Encoder) buildPredCoefQ12(predCoefQ12 []int16, nlsfQ15 []int16, interpIdx int) int {
	order := len(nlsfQ15)
	if order > maxLPCOrder {
		order = maxLPCOrder
	}

	// Clear the destination buffer to avoid stale coefficients.
	for i := range predCoefQ12 {
		predCoefQ12[i] = 0
	}

	// Compute LPC from current NLSF (goes into second set).
	curr := predCoefQ12[maxLPCOrder : maxLPCOrder+order]
	if !silkNLSF2A(curr, nlsfQ15[:order], order) {
		lpc := lsfToLPCDirect(nlsfQ15[:order])
		copy(curr, lpc[:order])
		interpIdx = 4
	}

	// Handle interpolation for first subframes when allowed.
	if interpIdx < 4 && e.haveEncoded {
		var interpNLSF [maxLPCOrder]int16
		for i := 0; i < order; i++ {
			diff := int32(nlsfQ15[i]) - int32(e.prevLSFQ15[i])
			interpNLSF[i] = int16(int32(e.prevLSFQ15[i]) + (int32(interpIdx)*diff >> 2))
		}
		first := predCoefQ12[:order]
		if !silkNLSF2A(first, interpNLSF[:order], order) {
			lpc := lsfToLPCDirect(interpNLSF[:order])
			copy(first, lpc[:order])
			interpIdx = 4
		}
	} else {
		interpIdx = 4
	}

	// If interpolation disabled, use current coefficients for both sets.
	if interpIdx >= 4 {
		copy(predCoefQ12[:order], predCoefQ12[maxLPCOrder:maxLPCOrder+order])
	}

	return interpIdx
}

// encodeLSF encodes quantized LSF to bitstream per libopus.
// Uses libopus ICDF tables matching silkDecodeIndices in libopus_decode.go.
// Per RFC 6716 Section 4.2.7.5.2.
func (e *Encoder) encodeLSF(stage1Idx int, residuals []int, interpIdx int, bandwidth Bandwidth, signalType int, numSubframes int) {
	isWideband := bandwidth == BandwidthWideband

	// Select codebook based on bandwidth
	var cb *nlsfCB
	if isWideband {
		cb = &silk_NLSF_CB_WB
	} else {
		cb = &silk_NLSF_CB_NB_MB
	}

	// Signal type band: 0 for inactive/unvoiced, 1 for voiced
	stypeBand := signalType >> 1

	// Clamp stage1 index to valid range
	if stage1Idx < 0 {
		stage1Idx = 0
	}
	if stage1Idx >= cb.nVectors {
		stage1Idx = cb.nVectors - 1
	}

	// Encode stage 1 index using cb.cb1ICDF[stypeBand*nVectors:]
	// This matches decoder: rd.DecodeICDF(cb.cb1ICDF[cb1Offset:], 8)
	cb1Offset := stypeBand * cb.nVectors
	e.rangeEncoder.EncodeICDF(stage1Idx, cb.cb1ICDF[cb1Offset:], 8)

	// Get ecIx for stage 2 encoding (same as silkNLSFUnpack) using scratch buffers
	ecIx := ensureInt16Slice(&e.scratchEcIx, cb.order)
	predQ8 := ensureUint8Slice(&e.scratchPredQ8, cb.order)
	silkNLSFUnpack(ecIx, predQ8, cb, stage1Idx)

	// Encode stage 2 residuals for each coefficient.
	// Match libopus silk/encode_indices.c boundary behavior exactly:
	// extension coding is used for idx <= -A and idx >= +A (including boundary).
	// This must mirror decoder logic, which always reads an extension symbol when
	// the decoded base symbol is 0 or 2*A.
	for i := 0; i < cb.order && i < len(residuals); i++ {
		idx := residuals[i]
		if idx >= nlsfQuantMaxAmplitude {
			e.rangeEncoder.EncodeICDF(2*nlsfQuantMaxAmplitude, cb.ecICDF[ecIx[i]:], 8)
			e.rangeEncoder.EncodeICDF(idx-nlsfQuantMaxAmplitude, silk_NLSF_EXT_iCDF, 8)
		} else if idx <= -nlsfQuantMaxAmplitude {
			e.rangeEncoder.EncodeICDF(0, cb.ecICDF[ecIx[i]:], 8)
			e.rangeEncoder.EncodeICDF(-idx-nlsfQuantMaxAmplitude, silk_NLSF_EXT_iCDF, 8)
		} else {
			e.rangeEncoder.EncodeICDF(idx+nlsfQuantMaxAmplitude, cb.ecICDF[ecIx[i]:], 8)
		}
	}

	// Encode interpolation index only for 20ms frames (4 subframes).
	// Per libopus silk/encode_indices.c: the interpolation factor is only
	// written when nb_subfr == MAX_NB_SUBFR.  The decoder (silk_decode_indices)
	// mirrors this: it only reads the symbol for 4-subframe packets.
	// For 10ms frames (2 subframes) the decoder hard-codes interpCoefQ2=4.
	if numSubframes == maxNbSubfr {
		if interpIdx < 0 {
			interpIdx = 0
		}
		if interpIdx > 4 {
			interpIdx = 4
		}
		e.rangeEncoder.EncodeICDF(interpIdx, silk_NLSF_interpolation_factor_iCDF, 8)
	}
}
