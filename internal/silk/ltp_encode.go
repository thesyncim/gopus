package silk

import "math"

// analyzeLTP computes LTP coefficients for each subframe.
// LTP predicts current samples from pitch-delayed past samples.
//
// Per draft-vos-silk-01 Section 2.1.2.6.
// Returns 5-tap LTP coefficients per subframe in Q7 format.
func (e *Encoder) analyzeLTP(pcm []float32, pitchLags []int, numSubframes int) [][]int8 {
	config := GetBandwidthConfig(e.bandwidth)
	subframeSamples := config.SubframeSamples

	ltpCoeffs := make([][]int8, numSubframes)

	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeSamples
		lag := pitchLags[sf]

		// Compute optimal LTP coefficients via least squares
		coeffs := computeLTPCoeffs(pcm, start, subframeSamples, lag)

		// Quantize to codebook
		ltpCoeffs[sf] = quantizeLTPCoeffs(coeffs, e.isPreviousFrameVoiced)
	}

	return ltpCoeffs
}

// computeLTPCoeffs computes 5-tap LTP coefficients for a subframe.
// Uses least-squares minimization of prediction error.
func computeLTPCoeffs(pcm []float32, start, length, lag int) []float64 {
	const numTaps = 5
	const halfTaps = 2

	// Compute autocorrelation matrix and cross-correlation vector
	// R[i][j] = sum(x[n-lag+i-2] * x[n-lag+j-2])
	// r[i] = sum(x[n] * x[n-lag+i-2])

	var R [numTaps][numTaps]float64
	var r [numTaps]float64

	for n := start; n < start+length; n++ {
		if n >= len(pcm) || n < lag+halfTaps {
			continue
		}

		x := float64(pcm[n])

		for i := 0; i < numTaps; i++ {
			pastIdx := n - lag + i - halfTaps
			if pastIdx < 0 || pastIdx >= len(pcm) {
				continue
			}
			pastI := float64(pcm[pastIdx])
			r[i] += x * pastI

			for j := 0; j < numTaps; j++ {
				pastJIdx := n - lag + j - halfTaps
				if pastJIdx < 0 || pastJIdx >= len(pcm) {
					continue
				}
				pastJ := float64(pcm[pastJIdx])
				R[i][j] += pastI * pastJ
			}
		}
	}

	// Regularization for stability
	for i := 0; i < numTaps; i++ {
		R[i][i] += 1e-6
	}

	// Solve R * coeffs = r using Gaussian elimination
	coeffs := solveLTPSystem(R, r)

	return coeffs[:]
}

// solveLTPSystem solves the 5x5 normal equations using Gaussian elimination.
func solveLTPSystem(R [5][5]float64, r [5]float64) [5]float64 {
	const n = 5

	// Augmented matrix
	var A [n][n + 1]float64
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			A[i][j] = R[i][j]
		}
		A[i][n] = r[i]
	}

	// Forward elimination with partial pivoting
	for i := 0; i < n; i++ {
		// Find pivot
		maxRow := i
		for k := i + 1; k < n; k++ {
			if math.Abs(A[k][i]) > math.Abs(A[maxRow][i]) {
				maxRow = k
			}
		}
		A[i], A[maxRow] = A[maxRow], A[i]

		if math.Abs(A[i][i]) < 1e-10 {
			continue // Skip singular
		}

		// Eliminate column
		for k := i + 1; k < n; k++ {
			factor := A[k][i] / A[i][i]
			for j := i; j <= n; j++ {
				A[k][j] -= factor * A[i][j]
			}
		}
	}

	// Back substitution
	var coeffs [5]float64
	for i := n - 1; i >= 0; i-- {
		sum := A[i][n]
		for j := i + 1; j < n; j++ {
			sum -= A[i][j] * coeffs[j]
		}
		if math.Abs(A[i][i]) > 1e-10 {
			coeffs[i] = sum / A[i][i]
		}
	}

	return coeffs
}

// quantizeLTPCoeffs quantizes LTP coefficients to nearest codebook entry.
// Uses LTP codebook from codebook.go (LTPFilterLow/Mid/High).
// Returns Q7 format coefficients.
func quantizeLTPCoeffs(coeffs []float64, isPreviousVoiced bool) []int8 {
	const numTaps = 5

	// Select codebook based on periodicity
	// 0 = low, 1 = mid, 2 = high
	periodicity := 1 // Default medium periodicity
	if isPreviousVoiced {
		periodicity = 2 // High periodicity for voiced continuation
	}

	// Find best matching codebook entry
	bestIdx := 0
	var bestDist float64 = math.MaxFloat64
	result := make([]int8, numTaps)

	switch periodicity {
	case 0:
		for idx := 0; idx < len(LTPFilterLow); idx++ {
			var dist float64
			for tap := 0; tap < numTaps; tap++ {
				cbVal := float64(LTPFilterLow[idx][tap]) / 128.0
				diff := coeffs[tap] - cbVal
				dist += diff * diff
			}
			if dist < bestDist {
				bestDist = dist
				bestIdx = idx
			}
		}
		copy(result, LTPFilterLow[bestIdx][:])

	case 1:
		for idx := 0; idx < len(LTPFilterMid); idx++ {
			var dist float64
			for tap := 0; tap < numTaps; tap++ {
				cbVal := float64(LTPFilterMid[idx][tap]) / 128.0
				diff := coeffs[tap] - cbVal
				dist += diff * diff
			}
			if dist < bestDist {
				bestDist = dist
				bestIdx = idx
			}
		}
		copy(result, LTPFilterMid[bestIdx][:])

	case 2:
		for idx := 0; idx < len(LTPFilterHigh); idx++ {
			var dist float64
			for tap := 0; tap < numTaps; tap++ {
				cbVal := float64(LTPFilterHigh[idx][tap]) / 128.0
				diff := coeffs[tap] - cbVal
				dist += diff * diff
			}
			if dist < bestDist {
				bestDist = dist
				bestIdx = idx
			}
		}
		copy(result, LTPFilterHigh[bestIdx][:])
	}

	return result
}

// encodeLTPCoeffs encodes LTP coefficients to the bitstream.
// Per RFC 6716 Section 4.2.7.6.3.
// Uses existing ICDF tables: ICDFLTPFilterIndex*, ICDFLTPGain*
//
// The encoding uses the periodicity computed from signal analysis to select
// the appropriate codebook and ICDF tables.
//
// Periodicity mapping:
//   - 0 = Low periodicity (less correlated/voiced)
//   - 1 = Mid periodicity
//   - 2 = High periodicity (strongly voiced)
func (e *Encoder) encodeLTPCoeffs(ltpCoeffs [][]int8, periodicity int, numSubframes int) {
	// Select codebook and ICDF based on actual periodicity
	var gainICDF []uint16
	var codebookPeriodicity int

	switch periodicity {
	case 0:
		// Low periodicity - encode index 0-3 from ICDFLTPFilterIndexLowPeriod
		cbIdx := findLTPCodebookIndex(ltpCoeffs[0], 0)
		if cbIdx > 3 {
			cbIdx = 3 // Clamp to valid range for low periodicity
		}
		e.rangeEncoder.EncodeICDF16(cbIdx, ICDFLTPFilterIndexLowPeriod, 8)
		gainICDF = ICDFLTPGainLow
		codebookPeriodicity = 0

	case 1:
		// Mid periodicity - signal via low period table first, then mid
		// Encode 0 in low period table (to indicate "not low")
		// Note: The decoder expects a multi-stage encoding for mid/high periodicity
		// For compatibility with current decoder, fall back to low periodicity
		e.rangeEncoder.EncodeICDF16(0, ICDFLTPFilterIndexLowPeriod, 8)
		gainICDF = ICDFLTPGainMid
		codebookPeriodicity = 1

	case 2:
		// High periodicity - strongly voiced
		// For compatibility with current decoder, fall back to low periodicity encoding
		// but use high periodicity codebook for better coefficient matching
		e.rangeEncoder.EncodeICDF16(0, ICDFLTPFilterIndexLowPeriod, 8)
		gainICDF = ICDFLTPGainHigh
		codebookPeriodicity = 2

	default:
		// Default to low periodicity
		e.rangeEncoder.EncodeICDF16(0, ICDFLTPFilterIndexLowPeriod, 8)
		gainICDF = ICDFLTPGainLow
		codebookPeriodicity = 0
	}

	// Encode codebook index per subframe using the selected periodicity codebook
	for sf := 0; sf < numSubframes; sf++ {
		// Find best matching codebook index for this subframe's coefficients
		cbIdx := findLTPCodebookIndex(ltpCoeffs[sf], codebookPeriodicity)
		// Clamp to valid range for ICDF table
		maxIdx := len(gainICDF) - 2
		if cbIdx > maxIdx {
			cbIdx = maxIdx
		}
		e.rangeEncoder.EncodeICDF16(cbIdx, gainICDF, 8)
	}
}

// findLTPCodebookIndex finds the codebook index for given coefficients.
func findLTPCodebookIndex(coeffs []int8, periodicity int) int {
	const numTaps = 5

	switch periodicity {
	case 0:
		for idx := 0; idx < len(LTPFilterLow); idx++ {
			match := true
			for tap := 0; tap < numTaps; tap++ {
				if coeffs[tap] != LTPFilterLow[idx][tap] {
					match = false
					break
				}
			}
			if match {
				return idx
			}
		}
	case 1:
		for idx := 0; idx < len(LTPFilterMid); idx++ {
			match := true
			for tap := 0; tap < numTaps; tap++ {
				if coeffs[tap] != LTPFilterMid[idx][tap] {
					match = false
					break
				}
			}
			if match {
				return idx
			}
		}
	case 2:
		for idx := 0; idx < len(LTPFilterHigh); idx++ {
			match := true
			for tap := 0; tap < numTaps; tap++ {
				if coeffs[tap] != LTPFilterHigh[idx][tap] {
					match = false
					break
				}
			}
			if match {
				return idx
			}
		}
	}

	return 0 // Default to first entry if no match
}

// determinePeriodicity determines LTP periodicity level based on signal characteristics.
// Returns 0 (low), 1 (mid), or 2 (high) periodicity.
func (e *Encoder) determinePeriodicity(pcm []float32, pitchLags []int) int {
	// Compute average normalized autocorrelation at pitch lag
	var totalCorr float64
	var count int

	config := GetBandwidthConfig(e.bandwidth)
	subframeSamples := config.SubframeSamples

	for sf, lag := range pitchLags {
		start := sf * subframeSamples
		end := start + subframeSamples
		if end > len(pcm) {
			end = len(pcm)
		}

		var corr, energy1, energy2 float64
		for i := start; i < end && i >= lag; i++ {
			s := float64(pcm[i])
			past := float64(pcm[i-lag])
			corr += s * past
			energy1 += s * s
			energy2 += past * past
		}

		if energy1 > 1e-10 && energy2 > 1e-10 {
			totalCorr += corr / math.Sqrt(energy1*energy2)
			count++
		}
	}

	if count == 0 {
		return 1 // Default to mid
	}

	avgCorr := totalCorr / float64(count)

	// Classify periodicity based on correlation strength
	if avgCorr < 0.5 {
		return 0 // Low periodicity
	} else if avgCorr < 0.8 {
		return 1 // Mid periodicity
	}
	return 2 // High periodicity
}
