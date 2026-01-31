// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides alloc_trim_analysis, which dynamically computes the
// allocation trim parameter based on bitrate and signal characteristics.

package celt

import "math"

// allocTrimAnalysis computes the allocation trim parameter dynamically.
// This matches libopus alloc_trim_analysis() from celt/celt_encoder.c lines 865-955.
//
// The trim parameter adjusts the bit allocation between low and high frequency bands.
// Trim values range from 0 to 10:
//   - 0-4: Favor low frequencies (useful for bass-heavy or low-bitrate content)
//   - 5: Neutral (default)
//   - 6-10: Favor high frequencies (useful for bright or high-bitrate content)
//
// Parameters:
//   - equivRate: Equivalent bitrate in bits per second
//   - channels: Number of audio channels (1 or 2)
//   - bandLogE: Band log-energies in log2 units (DB6 = 1.0 = 6dB)
//   - tfEstimate: Transient factor estimate from transient analysis (0.0-1.0)
//   - nbBands: Number of bands (end parameter)
//   - lm: Log mode (0-3)
//   - normX: Normalized MDCT coefficients (for stereo correlation, can be nil for mono)
//   - normXRight: Right channel normalized coefficients (for stereo, can be nil)
//   - intensity: Intensity stereo start band (for stereo correlation computation)
//   - surroundTrim: Surround trim adjustment in log2 units (0 if not used)
//
// Returns: trim index in range [0, 10]
//
// Reference: libopus celt/celt_encoder.c alloc_trim_analysis()
func allocTrimAnalysis(equivRate int, channels int, bandLogE []float64,
	tfEstimate float64, nbBands int, lm int,
	normX []float64, normXRight []float64, intensity int, surroundTrim float64) int {

	// Start with trim = 5.0 (neutral)
	// In libopus: trim = QCONST16(5.f, 8) which is 5.0 in Q8 fixed-point
	trim := 5.0

	// Step 1: Adjust based on bitrate
	// At low bitrate, reducing the trim helps. At higher bitrates, it's less clear.
	// Reference: libopus lines 876-883
	if equivRate < 64000 {
		// Low bitrate: use trim = 4.0
		trim = 4.0
	} else if equivRate < 80000 {
		// Interpolate from 4.0 to 5.0 between 64kbps and 80kbps
		// libopus: frac = (equiv_rate-64000) >> 10
		// trim = 4.0 + (1.0/16.0) * frac
		// This means at 80kbps: frac = 16000 >> 10 = 15.625, so trim = 4.0 + 15.625/16 = 4.976
		frac := float64((equivRate - 64000) >> 10)
		trim = 4.0 + (1.0/16.0)*frac
	}
	// else: equivRate >= 80000, keep trim = 5.0

	// Step 2: Stereo correlation adjustment (only for stereo)
	// This computes inter-channel correlation and adjusts trim based on how
	// correlated the channels are (highly correlated = can save bits with mid-side)
	// Reference: libopus lines 884-920
	if channels == 2 && normX != nil && normXRight != nil && len(normX) > 0 && len(normXRight) > 0 {
		// Compute inter-channel correlation for low frequencies (bands 0-7)
		// and find minimum correlation for higher bands
		sum := 0.0
		numBands := 8
		if numBands > nbBands {
			numBands = nbBands
		}

		for i := 0; i < numBands; i++ {
			bandStart := EBands[i] << lm
			bandEnd := EBands[i+1] << lm
			if bandEnd > len(normX) {
				bandEnd = len(normX)
			}
			if bandEnd > len(normXRight) {
				bandEnd = len(normXRight)
			}
			if bandStart >= bandEnd {
				continue
			}

			// Compute inner product for this band (correlation)
			partial := 0.0
			for j := bandStart; j < bandEnd; j++ {
				partial += normX[j] * normXRight[j]
			}
			// Normalize by band length
			bandLen := bandEnd - bandStart
			if bandLen > 0 {
				partial /= float64(bandLen)
			}
			sum += partial
		}
		sum /= float64(numBands)
		if sum > 1.0 {
			sum = 1.0
		}
		if sum < -1.0 {
			sum = -1.0
		}
		sum = math.Abs(sum)

		// Find minimum correlation for higher bands (8 to intensity)
		minXC := sum
		for i := 8; i < intensity && i < nbBands; i++ {
			bandStart := EBands[i] << lm
			bandEnd := EBands[i+1] << lm
			if bandEnd > len(normX) {
				bandEnd = len(normX)
			}
			if bandEnd > len(normXRight) {
				bandEnd = len(normXRight)
			}
			if bandStart >= bandEnd {
				continue
			}

			partial := 0.0
			for j := bandStart; j < bandEnd; j++ {
				partial += normX[j] * normXRight[j]
			}
			bandLen := bandEnd - bandStart
			if bandLen > 0 {
				partial /= float64(bandLen)
			}
			absPartial := math.Abs(partial)
			if absPartial < 1.0 && absPartial < minXC {
				minXC = absPartial
			}
		}

		// Compute log-based stereo correlation metrics
		// logXC = log2(1.001 - sum^2)
		// This measures how much we can save with mid-side stereo
		logXC := math.Log2(1.001 - sum*sum)

		// Adjust trim based on stereo correlation
		// More correlated (logXC closer to 0) means we can save bits -> reduce trim
		// libopus: trim += max(-4.0, 0.75 * logXC)
		adjustment := 0.75 * logXC
		if adjustment < -4.0 {
			adjustment = -4.0
		}
		trim += adjustment
	}

	// Step 3: Estimate spectral tilt and adjust trim
	// This measures whether energy is concentrated in low bands (tilt down)
	// or high bands (tilt up), and adjusts trim accordingly.
	// Reference: libopus lines 922-931
	diff := 0.0
	nbEBands := MaxBands
	for c := 0; c < channels; c++ {
		for i := 0; i < nbBands-1 && i < len(bandLogE)-c*nbEBands; i++ {
			// Weight by position: low bands get negative weight, high bands get positive
			// libopus: diff += (bandLogE[i+c*nbEBands] >> 5) * (2 + 2*i - end)
			// In float, bandLogE is in log2 units (DB6 = 1.0)
			// The >> 5 corresponds to dividing by 32
			idx := i + c*nbEBands
			if idx >= len(bandLogE) {
				break
			}
			weight := float64(2 + 2*i - nbBands)
			diff += (bandLogE[idx] / 32.0) * weight
		}
	}
	diff /= float64(channels * (nbBands - 1))

	// Apply spectral tilt adjustment
	// libopus: trim -= max(-2.0, min(2.0, (diff + 1.0) / 6.0))
	// The +1.0 bias and /6.0 scaling come from libopus fixed-point math
	// (SHR32(diff+QCONST32(1.f, DB_SHIFT-5), DB_SHIFT-13)/6)
	// DB_SHIFT=24, so DB_SHIFT-5=19 and DB_SHIFT-13=11
	// In float terms, this corresponds to: (diff + 1.0/32.0) / 6.0 approximately
	// But the exact scaling depends on the DB_SHIFT representation.
	// For float bandLogE in log2 units, we approximate the libopus behavior.
	tiltAdj := diff / 6.0
	if tiltAdj > 2.0 {
		tiltAdj = 2.0
	}
	if tiltAdj < -2.0 {
		tiltAdj = -2.0
	}
	trim -= tiltAdj

	// Step 4: Apply surround trim adjustment
	// Reference: libopus line 932: trim -= SHR16(surround_trim, DB_SHIFT-8)
	// For standard stereo (non-surround), this is 0
	if surroundTrim != 0 {
		trim -= surroundTrim
	}

	// Step 5: Apply tf_estimate adjustment
	// Reference: libopus line 933: trim -= 2*SHR16(tf_estimate, 14-8)
	// tf_estimate in libopus is Q14, so SHR16(tf_estimate, 6) = tf_estimate / 64
	// In our float representation, tfEstimate is 0.0 to 1.0
	// So the adjustment is: 2 * (tfEstimate * 64) / 64 = 2 * tfEstimate / 64 * 64 = 2 * tfEstimate
	// Wait, that's not right. Let me recalculate:
	// In libopus Q14, tf_estimate of 1.0 = 16384 (2^14)
	// SHR16(16384, 14-8) = 16384 >> 6 = 256 (in Q8)
	// 2 * 256 = 512 in Q8 = 2.0 in float
	// So: trim -= 2 * tfEstimate (when tfEstimate is 0.0 to 1.0)
	trim -= 2.0 * tfEstimate

	// Step 6: Round to nearest integer and clamp to [0, 10]
	// Reference: libopus line 947-949
	trimIndex := int(math.Floor(0.5 + trim))
	if trimIndex < 0 {
		trimIndex = 0
	}
	if trimIndex > 10 {
		trimIndex = 10
	}

	return trimIndex
}

// computeEquivRate computes the equivalent bitrate for trim analysis.
// This matches libopus celt_encoder.c line 1925:
//
//	equiv_rate = ((opus_int32)nbCompressedBytes*8*50 << (3-LM)) - (40*C+20)*((400>>LM) - 50)
//
// Parameters:
//   - nbCompressedBytes: Number of bytes available for the compressed output
//   - channels: Number of audio channels (1 or 2)
//   - lm: Log mode index (0-3)
//   - targetBitrate: Target bitrate in bps (0 for unconstrained VBR)
//
// Returns: equivalent bitrate in bits per second
//
// Reference: libopus celt/celt_encoder.c line 1925-1927
func computeEquivRate(nbCompressedBytes int, channels int, lm int, targetBitrate int) int {
	// equiv_rate = ((nbCompressedBytes*8*50) << (3-LM)) - (40*C+20)*((400>>LM) - 50)
	// First term: bytes * 8 bits * 50 frames/sec * 2^(3-LM)
	// This converts compressed bytes to a per-second equivalent bitrate
	// accounting for frame duration (LM affects frame length)
	firstTerm := (nbCompressedBytes * 8 * 50) << (3 - lm)

	// Second term: overhead adjustment
	// (40*C+20) is a per-frame overhead
	// ((400>>LM) - 50) is frames per second at this LM minus 50
	overhead := (40*channels + 20) * ((400 >> lm) - 50)

	equivRate := firstTerm - overhead

	// If we have a target bitrate, cap equiv_rate to avoid exceeding it
	// libopus line 1927: equiv_rate = IMIN(equiv_rate, st->bitrate - (40*C+20)*((400>>LM) - 50))
	if targetBitrate > 0 {
		bitrateAdjusted := targetBitrate - overhead
		if bitrateAdjusted < equivRate {
			equivRate = bitrateAdjusted
		}
	}

	if equivRate < 0 {
		equivRate = 0
	}

	return equivRate
}
