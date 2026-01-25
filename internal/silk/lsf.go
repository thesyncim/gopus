package silk

// decodeLSFCoefficients decodes LSF coefficients using two-stage VQ.
// Per RFC 6716 Section 4.2.7.5.
//
// Stage 1: Decode codebook index to get base LSF values.
// Stage 2: Decode residuals to refine each coefficient.
// Returns LSF in Q15 format [0, 32767] representing [0, pi].
func (d *Decoder) decodeLSFCoefficients(bandwidth Bandwidth, signalType int) []int16 {
	config := GetBandwidthConfig(bandwidth)
	lpcOrder := config.LPCOrder
	isWideband := bandwidth == BandwidthWideband
	isVoiced := signalType == 2

	lsfQ15 := make([]int16, lpcOrder)

	// Stage 1: Decode codebook index (selects base LSF vector)
	var stage1Idx int
	if isWideband {
		if isVoiced {
			stage1Idx = d.rangeDecoder.DecodeICDF16(ICDFLSFStage1WBVoiced, 8)
		} else {
			stage1Idx = d.rangeDecoder.DecodeICDF16(ICDFLSFStage1WBUnvoiced, 8)
		}
	} else {
		if isVoiced {
			stage1Idx = d.rangeDecoder.DecodeICDF16(ICDFLSFStage1NBMBVoiced, 8)
		} else {
			stage1Idx = d.rangeDecoder.DecodeICDF16(ICDFLSFStage1NBMBUnvoiced, 8)
		}
	}

	// Stage 2: Decode residual indices for each coefficient
	// The map index selects which residual codebook to use
	mapIdx := stage1Idx >> 2 // Maps 0-31 to 0-7

	residuals := make([]int, lpcOrder)
	for i := 0; i < lpcOrder; i++ {
		// Use shared stage 2 ICDF (same probabilities for all coefficients)
		var icdf []uint16
		if isWideband {
			icdf = ICDFLSFStage2WB[mapIdx]
		} else {
			icdf = ICDFLSFStage2NBMB[mapIdx]
		}
		residuals[i] = d.rangeDecoder.DecodeICDF16(icdf, 8)
	}

	// Decode interpolation index (for smoothing with previous frame)
	interpIdx := d.rangeDecoder.DecodeICDF16(ICDFLSFInterpolation, 8)

	// Reconstruct LSF: base codebook + stage 2 residual
	if isWideband {
		for i := 0; i < lpcOrder; i++ {
			// Base value from stage 1 codebook (Q8 scaled to Q15)
			base := int32(LSFCodebookWB[stage1Idx][i]) << 7

			// Add stage 2 residual
			res := int32(LSFStage2ResWB[mapIdx][residuals[i]][i]) << 7

			// Apply interpolation with previous frame
			lsfQ15[i] = int16(base + res)
		}
	} else {
		for i := 0; i < lpcOrder; i++ {
			// Base value from stage 1 codebook (Q8 scaled to Q15)
			base := int32(LSFCodebookNBMB[stage1Idx][i]) << 7

			// Add stage 2 residual
			res := int32(LSFStage2ResNBMB[mapIdx][residuals[i]][i]) << 7

			// Apply interpolation with previous frame
			lsfQ15[i] = int16(base + res)
		}
	}

	// Apply prediction from previous frame LSF
	// Per RFC 6716 Section 4.2.7.5.3
	d.applyLSFPrediction(lsfQ15, stage1Idx, interpIdx, isWideband)

	// Stabilize LSF (ensure minimum spacing and ordering)
	stabilizeLSF(lsfQ15, isWideband)

	// Update state for next frame
	copy(d.prevLSFQ15, lsfQ15)

	return lsfQ15
}

// applyLSFPrediction applies weighted prediction from previous frame LSF.
// Per RFC 6716 Section 4.2.7.5.3.
func (d *Decoder) applyLSFPrediction(lsf []int16, stage1Idx, interpIdx int, isWideband bool) {
	if interpIdx == 4 {
		// interpIdx=4 means no interpolation with previous frame
		return
	}

	// Interpolation weight = interpIdx / 4 (0, 0.25, 0.5, 0.75)
	// lsf = lsf * (1 - weight) + prevLSF * weight
	weight := int32(interpIdx) * 64 // Q8: 0, 64, 128, 192 for interpIdx 0-3

	lpcOrder := len(lsf)
	for i := 0; i < lpcOrder; i++ {
		// Blend current and previous LSF
		curr := int32(lsf[i]) * (256 - weight)
		prev := int32(d.prevLSFQ15[i]) * weight
		lsf[i] = int16((curr + prev + 128) >> 8)
	}
}

// stabilizeLSF ensures minimum spacing between adjacent LSF values.
// Per RFC 6716 Section 4.2.7.5.5.
//
// LSF values must be in increasing order with minimum gaps to ensure
// a stable LPC filter. Also clamps to [0, pi] range.
func stabilizeLSF(lsf []int16, isWideband bool) {
	lpcOrder := len(lsf)

	// Get minimum spacing table
	var minSpacing []int
	if isWideband {
		minSpacing = LSFMinSpacingWB[:]
	} else {
		minSpacing = LSFMinSpacingNBMB[:]
	}

	// First pass: enforce lower bound and minimum spacing from left
	minValue := int16(minSpacing[0])
	for i := 0; i < lpcOrder; i++ {
		if lsf[i] < minValue {
			lsf[i] = minValue
		}
		minValue = lsf[i] + int16(minSpacing[i+1])
	}

	// Second pass: enforce upper bound and minimum spacing from right
	maxValue := int16(32767 - minSpacing[lpcOrder])
	for i := lpcOrder - 1; i >= 0; i-- {
		if lsf[i] > maxValue {
			lsf[i] = maxValue
		}
		if i > 0 {
			maxValue = lsf[i] - int16(minSpacing[i])
		}
	}

	// Third pass: bubble sort to ensure strict ordering
	// (Should rarely be needed after spacing enforcement)
	for i := 0; i < lpcOrder-1; i++ {
		if lsf[i] > lsf[i+1] {
			tmp := lsf[i]
			lsf[i] = lsf[i+1]
			lsf[i+1] = tmp
		}
	}
}

// lsfToLPC converts LSF coefficients to LPC coefficients.
// Per RFC 6716 Section 4.2.7.5.6 using Chebyshev polynomial recursion.
//
// LSF input is in Q15 format [0, 32767].
// LPC output is in Q12 format.
func lsfToLPC(lsfQ15 []int16) []int16 {
	lpcOrder := len(lsfQ15)
	lpcQ12 := make([]int16, lpcOrder)

	// Convert LSF to cosines using CosineTable
	// LSF is in range [0, 32767] representing [0, pi]
	// Table index = LSF >> 8 (256 entries, but table has 129 for [0, pi])
	cos := make([]int32, lpcOrder)
	for i := 0; i < lpcOrder; i++ {
		// Map Q15 [0, 32767] to table index [0, 128]
		idx := int(lsfQ15[i]) >> 8
		if idx > 127 {
			idx = 127
		}
		frac := int32(lsfQ15[i]&0xFF) << 4 // Scale fraction to Q12

		// Linear interpolation between table entries
		tab0 := int32(silk_LSFCosTab_FIX_Q12[idx])
		tab1 := int32(silk_LSFCosTab_FIX_Q12[idx+1])
		cos[i] = tab0 + ((tab1-tab0)*frac+2048)>>12
	}

	// Compute LPC coefficients using Chebyshev polynomial recursion
	// Split into odd and even polynomials
	halfOrder := lpcOrder / 2

	// Temporary arrays for polynomial coefficients
	p := make([]int32, halfOrder+1) // Even polynomial
	q := make([]int32, halfOrder+1) // Odd polynomial

	// Initialize polynomials: p[0] = q[0] = 1.0 in Q12 = 4096
	p[0] = 4096
	q[0] = 4096

	// Build polynomials iteratively using Chebyshev recursion
	for i := 0; i < halfOrder; i++ {
		cosEven := cos[2*i]  // Even-indexed LSF
		cosOdd := cos[2*i+1] // Odd-indexed LSF

		// Update p polynomial with cos[2*i]
		// p[k+1] = p[k] - 2*cos*p[k] + p[k-1]
		for k := i + 1; k >= 1; k-- {
			p[k] = p[k] - ((cosEven*p[k-1] + 2048) >> 12) + p[k-1]
			if k > 1 {
				p[k] += p[k-2]
			}
		}

		// Update q polynomial with cos[2*i+1]
		for k := i + 1; k >= 1; k-- {
			q[k] = q[k] - ((cosOdd*q[k-1] + 2048) >> 12) + q[k-1]
			if k > 1 {
				q[k] += q[k-2]
			}
		}
	}

	// Combine P and Q polynomials to get LPC coefficients
	// a[k] = (p[k] + p[k-1] + q[k] - q[k-1]) / 2
	for i := 0; i < lpcOrder; i++ {
		k := i / 2
		if i%2 == 0 {
			if k < halfOrder {
				lpcQ12[i] = int16((p[k+1] + q[k+1]) >> 1)
			}
		} else {
			if k < halfOrder {
				lpcQ12[i] = int16((p[k+1] - q[k+1]) >> 1)
			}
		}
	}

	// Alternative simplified approach: direct conversion
	// This is a fallback implementation using the standard algorithm
	lpcQ12 = lsfToLPCDirect(lsfQ15)

	return lpcQ12
}

// lsfToLPCDirect converts LSF to LPC using the direct algorithm.
// Per RFC 6716 Section 4.2.7.5.6.
func lsfToLPCDirect(lsfQ15 []int16) []int16 {
	lpcOrder := len(lsfQ15)
	lpcQ12 := make([]int16, lpcOrder)

	// Convert LSF to cosines
	cos := make([]int32, lpcOrder)
	for i := 0; i < lpcOrder; i++ {
		idx := int(lsfQ15[i]) >> 8
		if idx > 127 {
			idx = 127
		}
		frac := int32(lsfQ15[i]&0xFF) * 16 // Scale to match table

		// Linear interpolation
		c0 := CosineTable[idx]
		c1 := CosineTable[idx+1]
		cos[i] = c0 + ((c1-c0)*frac+2048)>>12
	}

	// Compute polynomials (split odd/even)
	halfOrder := lpcOrder / 2

	// Initialize filter coefficients
	ff := make([]int32, lpcOrder+2) // Forward filter
	fb := make([]int32, lpcOrder+2) // Backward filter

	ff[0] = 4096 // Q12 = 1.0
	fb[0] = 4096

	// Build up the polynomial by adding one root at a time
	for i := 0; i < halfOrder; i++ {
		// Even root (contributes to ff)
		c := cos[2*i]
		for j := i + 1; j >= 1; j-- {
			// ff[j] = ff[j] - 2*c*ff[j-1]/4096 + ff[j-2]/4096*4096
			ff[j] = ff[j] - (c*ff[j-1]+2048)>>11 // >>11 for 2*c
			if j >= 2 {
				ff[j] += ff[j-2]
			}
		}

		// Odd root (contributes to fb)
		c = cos[2*i+1]
		for j := i + 1; j >= 1; j-- {
			fb[j] = fb[j] - (c*fb[j-1]+2048)>>11
			if j >= 2 {
				fb[j] += fb[j-2]
			}
		}
	}

	// Combine ff and fb to get LPC
	// a[k] = (ff[k] + ff[k+1] + fb[k] - fb[k+1]) / 2
	for i := 0; i < lpcOrder; i++ {
		k := (i + 1) / 2
		var val int32
		if i%2 == 0 {
			// Even index: use ff
			val = (ff[k] + ff[k+1]) >> 1
		} else {
			// Odd index: use fb
			val = (fb[k] + fb[k+1]) >> 1
		}

		// Clamp to Q12 range
		if val > 32767 {
			val = 32767
		}
		if val < -32768 {
			val = -32768
		}
		lpcQ12[i] = int16(val)
	}

	return lpcQ12
}
