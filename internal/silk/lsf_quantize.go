package silk

import "math"

// quantizeLSF quantizes LSF coefficients using two-stage VQ per libopus.
// Returns stage1 index, stage2 residuals (as NLSFIndices[1:order+1]), and interpolation index.
// Per RFC 6716 Section 4.2.7.5.
// Uses libopus tables from libopus_tables.go and libopus_codebook.go
func (e *Encoder) quantizeLSF(lsfQ15 []int16, bandwidth Bandwidth, signalType int) (int, []int, int) {
	isWideband := bandwidth == BandwidthWideband
	lpcOrder := len(lsfQ15)

	// Select codebook based on bandwidth
	var cb *nlsfCB
	if isWideband {
		cb = &silk_NLSF_CB_WB
	} else {
		cb = &silk_NLSF_CB_NB_MB
	}

	// Stage 1: Find best codebook entry
	stypeBand := signalType >> 1 // 0 for unvoiced/inactive, 1 for voiced
	bestStage1 := e.searchStage1CodebookLibopus(lsfQ15, cb, stypeBand)

	// Stage 2: Compute residual indices per coefficient
	residuals := e.computeStage2ResidualsLibopus(lsfQ15, bestStage1, cb)

	// Compute interpolation index (blend with previous frame)
	interpIdx := e.computeInterpolationIndex(lsfQ15, lpcOrder)

	return bestStage1, residuals, interpIdx
}

// searchStage1CodebookLibopus finds the best stage 1 codebook entry per libopus.
// Uses weighted distortion matching silkNLSFDecode's reconstruction.
func (e *Encoder) searchStage1CodebookLibopus(lsfQ15 []int16, cb *nlsfCB, stypeBand int) int {
	numCodewords := cb.nVectors
	order := cb.order

	// ICDF for rate calculation (offset by stypeBand * nVectors for voiced/unvoiced)
	icdf := cb.cb1ICDF[stypeBand*numCodewords:]

	bestIdx := 0
	var bestCost int64 = math.MaxInt64

	for idx := 0; idx < numCodewords; idx++ {
		// Compute weighted distortion
		var dist int64
		baseIdx := idx * order
		for i := 0; i < order; i++ {
			// Reconstruct what decoder would produce: base << 7 (Q8 to Q15)
			cbVal := int64(cb.cb1NLSFQ8[baseIdx+i]) << 7
			diff := int64(lsfQ15[i]) - cbVal

			// Use codebook weights for perceptual weighting
			weight := int64(cb.cb1WghtQ9[baseIdx+i])
			if weight == 0 {
				weight = 256
			}
			dist += (diff * diff) / weight
		}

		// Add rate cost from ICDF
		rate := e.computeSymbolRate8(idx, icdf)
		totalCost := dist + int64(rate*64) // Scale rate contribution

		if totalCost < bestCost {
			bestCost = totalCost
			bestIdx = idx
		}
	}

	return bestIdx
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

// computeStage2ResidualsLibopus computes stage 2 residual indices per libopus.
// These are the NLSFIndices[1:order+1] values that get encoded.
// Per libopus silk_NLSF_encode(): residuals are computed per coefficient using
// prediction and quantization step.
func (e *Encoder) computeStage2ResidualsLibopus(lsfQ15 []int16, stage1Idx int, cb *nlsfCB) []int {
	order := cb.order
	residuals := make([]int, order)

	// Get ecIx and predQ8 for this stage1 index (same as decoder's silkNLSFUnpack)
	ecIx := make([]int16, order)
	predQ8 := make([]uint8, order)
	silkNLSFUnpack(ecIx, predQ8, cb, stage1Idx)

	// Get base values from stage 1 codebook
	baseIdx := stage1Idx * order

	// Compute target residuals (what decoder needs to reconstruct lsfQ15)
	// Per libopus silk_NLSF_encode():
	// resQ10[i] = (lsfQ15[i] - base<<7) * weight / (1<<14) / quantStepSize
	// Then quantize to get index

	// First convert lsfQ15 to resQ10 (what we want to encode)
	resQ10 := make([]int16, order)
	for i := 0; i < order; i++ {
		// Target NLSF in Q15
		target := int32(lsfQ15[i])

		// Base from codebook (Q8 scaled to Q15)
		base := int32(cb.cb1NLSFQ8[baseIdx+i]) << 7

		// Difference in Q15
		diff := target - base

		// Apply weight (cb1WghtQ9) to get resQ10
		// Per libopus: resQ10 = diff * wght / (1 << 14)
		wght := int32(cb.cb1WghtQ9[baseIdx+i])
		if wght == 0 {
			wght = 256
		}
		resQ10[i] = int16((diff * wght) >> 14)
	}

	// Quantize residuals using prediction (reverse of silkNLSFResidualDequant)
	// Per libopus silk_NLSF_residual_quant():
	// The quantization is done in reverse order with prediction from next coefficient
	invQuantStepQ6 := cb.invQuantStepSizeQ6

	var outQ10 int32
	for i := order - 1; i >= 0; i-- {
		// Prediction from previous output
		predQ10 := silkRSHIFT(silkSMULBB(outQ10, int32(predQ8[i])), 8)

		// Target after removing prediction
		targetQ10 := int32(resQ10[i]) - predQ10

		// Quantize: idx = round(targetQ10 * invQuantStepQ6 / (1<<16))
		// The quantization maps to range [-nlsfQuantMaxAmplitude, +nlsfQuantMaxAmplitude]
		idx := silkRSHIFT_ROUND(silkSMULBB(targetQ10, int32(invQuantStepQ6)), 16)

		// Clamp to valid range
		if idx < -nlsfQuantMaxAmplitude {
			idx = -nlsfQuantMaxAmplitude
		}
		if idx > nlsfQuantMaxAmplitude {
			idx = nlsfQuantMaxAmplitude
		}

		residuals[i] = int(idx)

		// Reconstruct what decoder will compute (for next iteration's prediction)
		outQ10 = int32(idx) << 10
		if outQ10 > 0 {
			outQ10 -= nlsfQuantLevelAdjQ10
		} else if outQ10 < 0 {
			outQ10 += nlsfQuantLevelAdjQ10
		}
		outQ10 = silkSMLAWB(predQ10, outQ10, int32(cb.quantStepSizeQ16))
	}

	return residuals
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

// encodeLSF encodes quantized LSF to bitstream per libopus.
// Uses libopus ICDF tables matching silkDecodeIndices in libopus_decode.go.
// Per RFC 6716 Section 4.2.7.5.2.
func (e *Encoder) encodeLSF(stage1Idx int, residuals []int, interpIdx int, bandwidth Bandwidth, signalType int) {
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

	// Get ecIx for stage 2 encoding (same as silkNLSFUnpack)
	ecIx := make([]int16, cb.order)
	predQ8 := make([]uint8, cb.order)
	silkNLSFUnpack(ecIx, predQ8, cb, stage1Idx)

	// Encode stage 2 residuals for each coefficient
	// This matches decoder: rd.DecodeICDF(cb.ecICDF[ecIx[i]:], 8)
	for i := 0; i < cb.order && i < len(residuals); i++ {
		// Residual is in range [-nlsfQuantMaxAmplitude, +nlsfQuantMaxAmplitude]
		// Encode as index in [0, 2*nlsfQuantMaxAmplitude]
		resIdx := residuals[i] + nlsfQuantMaxAmplitude

		// Check for extension coding (values outside normal range)
		if resIdx < 0 {
			// Encode 0 (underflow marker), then extension
			e.rangeEncoder.EncodeICDF(0, cb.ecICDF[ecIx[i]:], 8)
			extVal := -resIdx
			if extVal > 6 {
				extVal = 6
			}
			e.rangeEncoder.EncodeICDF(extVal, silk_NLSF_EXT_iCDF, 8)
		} else if resIdx > 2*nlsfQuantMaxAmplitude {
			// Encode 2*nlsfQuantMaxAmplitude (overflow marker), then extension
			e.rangeEncoder.EncodeICDF(2*nlsfQuantMaxAmplitude, cb.ecICDF[ecIx[i]:], 8)
			extVal := resIdx - 2*nlsfQuantMaxAmplitude
			if extVal > 6 {
				extVal = 6
			}
			e.rangeEncoder.EncodeICDF(extVal, silk_NLSF_EXT_iCDF, 8)
		} else {
			// Normal range encoding
			e.rangeEncoder.EncodeICDF(resIdx, cb.ecICDF[ecIx[i]:], 8)
		}
	}

	// Encode interpolation index using silk_NLSF_interpolation_factor_iCDF
	// This matches decoder: rd.DecodeICDF(silk_NLSF_interpolation_factor_iCDF, 8)
	if interpIdx < 0 {
		interpIdx = 0
	}
	if interpIdx > 4 {
		interpIdx = 4
	}
	e.rangeEncoder.EncodeICDF(interpIdx, silk_NLSF_interpolation_factor_iCDF, 8)
}
