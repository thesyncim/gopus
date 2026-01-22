package silk

import "math"

// quantizeLSF quantizes LSF coefficients using two-stage VQ.
// Returns stage1 index, stage2 residuals, and interpolation index.
// Per RFC 6716 Section 4.2.7.5.
// Uses existing codebooks from codebook.go and ICDF tables from tables.go
func (e *Encoder) quantizeLSF(lsfQ15 []int16, bandwidth Bandwidth, signalType int) (int, []int, int) {
	isWideband := bandwidth == BandwidthWideband
	isVoiced := signalType == 2
	lpcOrder := len(lsfQ15)

	// Stage 1: Find best codebook entry with rate-distortion optimization
	bestStage1, _ := e.searchStage1Codebook(lsfQ15, isWideband, isVoiced, lpcOrder)

	// Stage 2: Compute and quantize residuals
	residuals := e.computeStage2Residuals(lsfQ15, bestStage1, isWideband, lpcOrder)

	// Compute interpolation index (blend with previous frame)
	interpIdx := e.computeInterpolationIndex(lsfQ15, lpcOrder)

	return bestStage1, residuals, interpIdx
}

// searchStage1Codebook finds the best stage 1 codebook entry.
// Uses weighted distortion with rate cost.
// Uses existing ICDF tables for rate calculation
func (e *Encoder) searchStage1Codebook(lsfQ15 []int16, isWideband, isVoiced bool, lpcOrder int) (int, int64) {
	// Lambda for rate-distortion tradeoff
	const lambda = 1.0

	// Select ICDF for rate calculation (from tables.go)
	var icdf []uint16
	var numCodewords int
	if isWideband {
		numCodewords = 32
		if isVoiced {
			icdf = ICDFLSFStage1WBVoiced
		} else {
			icdf = ICDFLSFStage1WBUnvoiced
		}
	} else {
		numCodewords = 32
		if isVoiced {
			icdf = ICDFLSFStage1NBMBVoiced
		} else {
			icdf = ICDFLSFStage1NBMBUnvoiced
		}
	}

	// Limit search to valid ICDF range (symbol count = len(icdf) - 1)
	// Valid symbols are 0 to len(icdf)-2
	maxSymbol := len(icdf) - 2
	if maxSymbol < 0 {
		maxSymbol = 0
	}
	if numCodewords > maxSymbol+1 {
		numCodewords = maxSymbol + 1
	}

	// Start from symbol 1 because symbol 0 has zero probability in SILK ICDF tables
	// (icdf[0] = 256 means probability 0)
	bestIdx := 1
	var bestCost int64 = math.MaxInt64

	for idx := 1; idx < numCodewords; idx++ {
		// Compute weighted distortion
		var dist int64
		for i := 0; i < lpcOrder; i++ {
			target := int64(lsfQ15[i])
			var cbVal int64
			if isWideband {
				cbVal = int64(LSFCodebookWB[idx][i]) << 7 // Scale to Q15
			} else {
				cbVal = int64(LSFCodebookNBMB[idx][i]) << 7
			}

			diff := target - cbVal
			// Perceptual weighting (higher weight at formant frequencies)
			weight := e.computeLSFWeight(i, lpcOrder)
			dist += (diff * diff * int64(weight)) >> 8
		}

		// Add rate cost from ICDF
		rate := e.computeSymbolRate(idx, icdf)

		totalCost := dist + int64(lambda*float64(rate))

		if totalCost < bestCost {
			bestCost = totalCost
			bestIdx = idx
		}
	}

	return bestIdx, bestCost
}

// computeStage2Residuals computes stage 2 residual indices.
// Uses existing LSFStage2Res* codebooks from codebook.go
func (e *Encoder) computeStage2Residuals(lsfQ15 []int16, stage1Idx int, isWideband bool, lpcOrder int) []int {
	residuals := make([]int, lpcOrder)
	mapIdx := stage1Idx >> 2 // Maps 0-31 to 0-7
	if mapIdx > 7 {
		mapIdx = 7
	}

	for i := 0; i < lpcOrder; i++ {
		var base int
		if isWideband {
			base = int(LSFCodebookWB[stage1Idx][i]) << 7
		} else {
			base = int(LSFCodebookNBMB[stage1Idx][i]) << 7
		}
		target := int(lsfQ15[i]) - base

		// Find best residual quantizer
		bestRes := 0
		bestDist := int(math.MaxInt32)

		// Residual codebooks have 9 entries each
		numResiduals := 9
		if isWideband {
			for resIdx := 0; resIdx < numResiduals; resIdx++ {
				if i >= 16 {
					continue
				}
				resVal := int(LSFStage2ResWB[mapIdx][resIdx][i]) << 7
				dist := absInt(target - resVal)
				if dist < bestDist {
					bestDist = dist
					bestRes = resIdx
				}
			}
		} else {
			for resIdx := 0; resIdx < numResiduals; resIdx++ {
				if i >= 10 {
					continue
				}
				resVal := int(LSFStage2ResNBMB[mapIdx][resIdx][i]) << 7
				dist := absInt(target - resVal)
				if dist < bestDist {
					bestDist = dist
					bestRes = resIdx
				}
			}
		}

		residuals[i] = bestRes
	}

	return residuals
}

// computeLSFWeight computes perceptual weight for LSF coefficient.
// Higher weight near formant frequencies.
func (e *Encoder) computeLSFWeight(idx, order int) int {
	// Simple weighting: higher in mid-range (formant region)
	midIdx := order / 2
	dist := absInt(idx - midIdx)
	weight := 256 - dist*16
	if weight < 64 {
		weight = 64
	}
	return weight
}

// computeSymbolRate estimates bit cost from ICDF probabilities.
func (e *Encoder) computeSymbolRate(symbol int, icdf []uint16) int {
	if symbol < 0 || symbol >= len(icdf)-1 {
		return 256 // Max cost for invalid symbols
	}

	// Rate ~ -log2(probability)
	var prob uint16
	if symbol == 0 {
		prob = 256 - icdf[0]
	} else {
		prob = icdf[symbol-1] - icdf[symbol]
	}

	if prob == 0 {
		return 256
	}

	// Approximate -log2(prob/256) * 8 (in 1/8 bits)
	// log2(256/prob) = 8 - log2(prob)
	rate := 8*8 - int(math.Log2(float64(prob))*8)
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

// encodeLSF encodes quantized LSF to bitstream.
// Uses existing ICDF tables from tables.go
// Per RFC 6716 Section 4.2.7.5.2, encode stage1 index,
// then stage2 residual indices (one per order dimension mapping).
func (e *Encoder) encodeLSF(stage1Idx int, residuals []int, interpIdx int, bandwidth Bandwidth, signalType int) {
	isWideband := bandwidth == BandwidthWideband
	isVoiced := signalType == 2

	// Clamp stage1 index to valid range for ICDF tables
	var maxStage1 int
	if isWideband {
		if isVoiced {
			maxStage1 = len(ICDFLSFStage1WBVoiced) - 2
		} else {
			maxStage1 = len(ICDFLSFStage1WBUnvoiced) - 2
		}
	} else {
		if isVoiced {
			maxStage1 = len(ICDFLSFStage1NBMBVoiced) - 2
		} else {
			maxStage1 = len(ICDFLSFStage1NBMBUnvoiced) - 2
		}
	}
	if stage1Idx < 0 {
		stage1Idx = 0
	}
	if stage1Idx > maxStage1 {
		stage1Idx = maxStage1
	}

	// Encode stage 1 index using appropriate ICDF
	if isWideband {
		if isVoiced {
			e.rangeEncoder.EncodeICDF16(stage1Idx, ICDFLSFStage1WBVoiced, 8)
		} else {
			e.rangeEncoder.EncodeICDF16(stage1Idx, ICDFLSFStage1WBUnvoiced, 8)
		}
	} else {
		if isVoiced {
			e.rangeEncoder.EncodeICDF16(stage1Idx, ICDFLSFStage1NBMBVoiced, 8)
		} else {
			e.rangeEncoder.EncodeICDF16(stage1Idx, ICDFLSFStage1NBMBUnvoiced, 8)
		}
	}

	// Encode stage 2 residuals using ICDFLSFStage2* tables
	// Per RFC 6716, we encode a few residual symbols (not one per coefficient)
	// The residuals slice contains indices into the stage2 codebook
	mapIdx := stage1Idx >> 2
	if mapIdx > 7 {
		mapIdx = 7
	}
	if mapIdx < 0 {
		mapIdx = 0
	}

	// Encode only a limited number of stage 2 residuals
	// The ICDF tables have 6 symbols (entries - 1)
	numResiduals := 3 // Typical for SILK stage 2
	if len(residuals) < numResiduals {
		numResiduals = len(residuals)
	}

	for i := 0; i < numResiduals; i++ {
		var icdf []uint16
		if isWideband {
			icdf = ICDFLSFStage2WB[mapIdx]
		} else {
			icdf = ICDFLSFStage2NBMB[mapIdx]
		}
		// Clamp residual index to valid range [0, len-2]
		resIdx := residuals[i]
		if resIdx < 0 {
			resIdx = 0
		}
		maxResIdx := len(icdf) - 2
		if maxResIdx < 0 {
			maxResIdx = 0
		}
		if resIdx > maxResIdx {
			resIdx = maxResIdx
		}
		e.rangeEncoder.EncodeICDF16(resIdx, icdf, 8)
	}

	// Encode interpolation index (clamped to valid range)
	maxInterpIdx := len(ICDFLSFInterpolation) - 2
	if interpIdx < 0 {
		interpIdx = 0
	}
	if interpIdx > maxInterpIdx {
		interpIdx = maxInterpIdx
	}
	e.rangeEncoder.EncodeICDF16(interpIdx, ICDFLSFInterpolation, 8)
}
