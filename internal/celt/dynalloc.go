// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file implements dynamic bit allocation analysis (dynalloc_analysis).

package celt

import (
	"math"
)

// EMeans contains the mean log-energy per band in float64 format.
// These values are in log2 units (1.0 = 6 dB) and represent typical
// energy distribution across frequency bands.
// Source: libopus celt/quant_bands.c (float eMeans table, lines 56-62)
var EMeans = [25]float64{
	6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
	4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
	4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
	4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
	3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
}

// DynallocResult contains the output of dynalloc_analysis.
// These values are used for VBR target computation and bit allocation.
type DynallocResult struct {
	// MaxDepth is the maximum signal level relative to noise floor (in dB).
	// Used for floor_depth calculation in VBR.
	// Reference: libopus celt_encoder.c lines 1682-1693
	MaxDepth float64

	// Offsets contains per-band allocation offsets for dynamic bit allocation.
	// Bands with high energy variance get extra bits.
	Offsets []int

	// SpreadWeight contains per-band masking weights for spread decision.
	// Higher values indicate more perceptually important bands.
	SpreadWeight []int

	// Importance contains per-band importance values (0-13 typically).
	// Used for bit allocation prioritization.
	Importance []int

	// TotBoost is the total boost in bits (Q3 format).
	// Represents extra bits allocated beyond base target.
	TotBoost int
}

// medianOf3 computes the median of 3 consecutive values starting at x.
// Reference: libopus celt/celt_encoder.c lines 1029-1047
func medianOf3(x []float64) float64 {
	if len(x) < 3 {
		if len(x) == 0 {
			return 0
		}
		return x[0]
	}

	var t0, t1, t2 float64
	if x[0] > x[1] {
		t0 = x[1]
		t1 = x[0]
	} else {
		t0 = x[0]
		t1 = x[1]
	}
	t2 = x[2]

	if t1 < t2 {
		return t1
	} else if t0 < t2 {
		return t2
	}
	return t0
}

// medianOf5 computes the median of 5 consecutive values starting at x.
// Reference: libopus celt/celt_encoder.c lines 990-1027
func medianOf5(x []float64) float64 {
	if len(x) < 5 {
		return medianOf3(x)
	}

	var t0, t1, t2, t3, t4 float64
	t2 = x[2]

	if x[0] > x[1] {
		t0 = x[1]
		t1 = x[0]
	} else {
		t0 = x[0]
		t1 = x[1]
	}

	if x[3] > x[4] {
		t3 = x[4]
		t4 = x[3]
	} else {
		t3 = x[3]
		t4 = x[4]
	}

	// Swap to ensure t0 <= t3
	if t0 > t3 {
		t0, t3 = t3, t0
		t1, t4 = t4, t1
	}

	if t2 > t1 {
		if t1 < t3 {
			return math.Min(t2, t3)
		}
		return math.Min(t4, t1)
	}
	if t2 < t3 {
		return math.Min(t1, t3)
	}
	return math.Min(t2, t4)
}

// computeNoiseFloor computes the noise floor for a given band.
// The noise floor accounts for:
// - Band width (logN)
// - Bit depth (lsbDepth)
// - Mean energy per band (eMeans)
// - Preemphasis adjustment: 0.0062 * (i+5)^2
//
// Reference: libopus celt/celt_encoder.c lines 1071-1075
func computeNoiseFloor(i, lsbDepth int, logN int16) float64 {
	eMean := 0.0
	if i < len(EMeans) {
		eMean = EMeans[i]
	}

	// noise_floor = 0.0625*logN + 0.5 + (9-lsb_depth) - eMeans + 0.0062*(i+5)^2
	// Note: logN is in Q8 format (multiplied by 256), so 0.0625 = 1/16 converts it
	return 0.0625*float64(logN) + 0.5 + float64(9-lsbDepth) - eMean + 0.0062*float64((i+5)*(i+5))
}

// DynallocAnalysis performs dynamic allocation analysis to compute:
// 1. maxDepth: signal depth relative to noise floor (for VBR floor_depth)
// 2. offsets: per-band bit allocation offsets
// 3. spread_weight: per-band masking weights for spread decision
// 4. importance: per-band importance for allocation prioritization
// 5. tot_boost: total boost bits for VBR target
//
// Parameters:
//   - bandLogE: current frame band energies (log2 domain), [channels * nbBands]
//   - bandLogE2: secondary band energies (from second MDCT for transients), [channels * nbBands]
//   - oldBandE: previous frame band energies, [channels * nbBands]
//   - nbBands: number of frequency bands
//   - start: starting band (usually 0)
//   - end: ending band (usually nbBands or less)
//   - channels: number of audio channels (1 or 2)
//   - lsbDepth: bit depth of input (16-24)
//   - lm: log2 of frame size multiplier (0=2.5ms, 1=5ms, 2=10ms, 3=20ms)
//   - logN: per-band log2 of width in Q8 format
//   - effectiveBytes: total available bytes for encoding
//   - isTransient: true if frame is transient
//   - vbr: true if using variable bitrate
//   - constrainedVBR: true if using constrained VBR
//   - toneFreq: detected tone frequency in radians/sample (-1 if none)
//   - toneishness: tone purity metric (0.0-1.0)
//
// Reference: libopus celt/celt_encoder.c lines 1049-1273
func DynallocAnalysis(
	bandLogE, bandLogE2, oldBandE []float64,
	nbBands, start, end, channels, lsbDepth, lm int,
	logN []int16,
	effectiveBytes int,
	isTransient, vbr, constrainedVBR bool,
	toneFreq, toneishness float64,
) DynallocResult {
	result := DynallocResult{
		MaxDepth:     -31.9,
		Offsets:      make([]int, nbBands),
		SpreadWeight: make([]int, nbBands),
		Importance:   make([]int, nbBands),
		TotBoost:     0,
	}

	// Compute noise floor per band
	noiseFloor := make([]float64, end)
	for i := 0; i < end; i++ {
		logNVal := int16(0)
		if i < len(logN) {
			logNVal = logN[i]
		}
		noiseFloor[i] = computeNoiseFloor(i, lsbDepth, logNVal)
	}

	// Compute maxDepth: max(bandLogE - noiseFloor) across all bands and channels
	for c := 0; c < channels; c++ {
		for i := 0; i < end; i++ {
			idx := c*nbBands + i
			if idx < len(bandLogE) {
				depth := bandLogE[idx] - noiseFloor[i]
				if depth > result.MaxDepth {
					result.MaxDepth = depth
				}
			}
		}
	}

	// Compute spread_weight using a simple masking model
	// Reference: libopus lines 1082-1117
	{
		mask := make([]float64, nbBands)
		sig := make([]float64, nbBands)

		// Initialize mask with signal relative to noise floor
		for i := 0; i < end; i++ {
			if i < len(bandLogE) {
				mask[i] = bandLogE[i] - noiseFloor[i]
			}
		}

		// For stereo, take max across channels
		if channels == 2 {
			for i := 0; i < end; i++ {
				idx := nbBands + i
				if idx < len(bandLogE) {
					ch2Val := bandLogE[idx] - noiseFloor[i]
					if ch2Val > mask[i] {
						mask[i] = ch2Val
					}
				}
			}
		}

		copy(sig, mask)

		// Forward masking: mask[i] = max(mask[i], mask[i-1] - 2)
		for i := 1; i < end; i++ {
			if mask[i-1]-2.0 > mask[i] {
				mask[i] = mask[i-1] - 2.0
			}
		}

		// Backward masking: mask[i] = max(mask[i], mask[i+1] - 3)
		for i := end - 2; i >= 0; i-- {
			if mask[i+1]-3.0 > mask[i] {
				mask[i] = mask[i+1] - 3.0
			}
		}

		// Compute SMR (Signal to Mask Ratio) and spread weight
		for i := 0; i < end; i++ {
			// Mask is never more than 72 dB below peak and never below noise floor
			maskThresh := math.Max(0, math.Max(result.MaxDepth-12.0, mask[i]))
			smr := sig[i] - maskThresh

			// Clamp shift to [0, 5] range
			shift := int(math.Max(0, math.Min(5, math.Floor(0.5-smr))))
			result.SpreadWeight[i] = 32 >> shift
		}
	}

	// Make sure dynamic allocation doesn't bust the budget
	// Enable starting at 24 kb/s for 20ms frames, 96 kb/s for 2.5ms frames
	// Reference: libopus line 1121
	minBytes := 30 + 5*lm
	if effectiveBytes >= minBytes {
		// Compute follower (smoothed band energies for dynamic allocation)
		follower := make([]float64, channels*nbBands)

		for c := 0; c < channels; c++ {
			// Use bandLogE2 (secondary MDCT for transients) or fallback to bandLogE
			bandLogE3 := make([]float64, end)
			for i := 0; i < end; i++ {
				idx := c*nbBands + i
				if bandLogE2 != nil && idx < len(bandLogE2) {
					bandLogE3[i] = bandLogE2[idx]
				} else if idx < len(bandLogE) {
					bandLogE3[i] = bandLogE[idx]
				}
			}

			// For 2.5ms frames (LM=0), first 8 bands have high variance
			// Take max with previous energy for stability
			if lm == 0 {
				for i := 0; i < min(8, end); i++ {
					idx := c*nbBands + i
					if oldBandE != nil && idx < len(oldBandE) {
						if oldBandE[idx] > bandLogE3[i] {
							bandLogE3[i] = oldBandE[idx]
						}
					}
				}
			}

			f := follower[c*nbBands : (c+1)*nbBands]
			if end > 0 {
				f[0] = bandLogE3[0]
			}

			// Forward pass: find last band at least 3dB higher than previous
			last := 0
			for i := 1; i < end; i++ {
				if bandLogE3[i] > bandLogE3[i-1]+0.5 {
					last = i
				}
				f[i] = math.Min(f[i-1]+1.5, bandLogE3[i])
			}

			// Backward pass: smooth from the last significant band
			for i := last - 1; i >= 0; i-- {
				f[i] = math.Min(f[i], math.Min(f[i+1]+2.0, bandLogE3[i]))
			}

			// Apply median filter to avoid unnecessary dynalloc triggering
			offset := 1.0
			for i := 2; i < end-2; i++ {
				medVal := medianOf5(bandLogE3[i-2:])
				if medVal-offset > f[i] {
					f[i] = medVal - offset
				}
			}

			// Handle edge bands with median of 3
			if end >= 3 {
				tmp := medianOf3(bandLogE3[0:3]) - offset
				if tmp > f[0] {
					f[0] = tmp
				}
				if tmp > f[1] {
					f[1] = tmp
				}

				tmp = medianOf3(bandLogE3[end-3:end]) - offset
				if tmp > f[end-2] {
					f[end-2] = tmp
				}
				if tmp > f[end-1] {
					f[end-1] = tmp
				}
			}

			// Clamp to noise floor
			for i := 0; i < end; i++ {
				if noiseFloor[i] > f[i] {
					f[i] = noiseFloor[i]
				}
			}
		}

		// For stereo: consider cross-talk (24 dB)
		if channels == 2 {
			for i := start; i < end; i++ {
				// Cross-channel influence
				ch0 := follower[i]
				ch1 := follower[nbBands+i]
				if ch0-4.0 > ch1 {
					follower[nbBands+i] = ch0 - 4.0
				}
				if ch1-4.0 > ch0 {
					follower[i] = ch1 - 4.0
				}

				// Combine channels: average of (bandLogE - follower) for each channel
				boost0 := 0.0
				boost1 := 0.0
				if i < len(bandLogE) {
					boost0 = math.Max(0, bandLogE[i]-follower[i])
				}
				if nbBands+i < len(bandLogE) {
					boost1 = math.Max(0, bandLogE[nbBands+i]-follower[nbBands+i])
				}
				follower[i] = (boost0 + boost1) / 2.0
			}
		} else {
			for i := start; i < end; i++ {
				if i < len(bandLogE) {
					follower[i] = math.Max(0, bandLogE[i]-follower[i])
				}
			}
		}

		// Compute importance weights
		for i := start; i < end; i++ {
			// importance = round(13 * 2^(min(follower, 4)))
			expArg := math.Min(follower[i], 4.0)
			result.Importance[i] = int(math.Floor(0.5 + 13.0*math.Pow(2.0, expArg)))
		}

		// For non-transient CBR/CVBR frames, halve the dynalloc contribution
		if (!vbr || constrainedVBR) && !isTransient {
			for i := start; i < end; i++ {
				follower[i] /= 2.0
			}
		}

		// Frequency-dependent weighting
		for i := start; i < end; i++ {
			if i < 8 {
				follower[i] *= 2.0
			}
			if i >= 12 {
				follower[i] /= 2.0
			}
		}

		// Compensate for Opus under-allocation on tones.
		if toneishness > 0.98 && toneFreq >= 0 {
			freqBin := int(math.Floor(0.5 + toneFreq*120.0/math.Pi))
			for i := start; i < end; i++ {
				if freqBin >= EBands[i] && freqBin <= EBands[i+1] {
					follower[i] += 2.0
				}
				if freqBin >= EBands[i]-1 && freqBin <= EBands[i+1]+1 {
					follower[i] += 1.0
				}
				if freqBin >= EBands[i]-2 && freqBin <= EBands[i+1]+2 {
					follower[i] += 1.0
				}
				if freqBin >= EBands[i]-3 && freqBin <= EBands[i+1]+3 {
					follower[i] += 0.5
				}
			}
			if end > start && freqBin >= EBands[end] {
				follower[end-1] += 2.0
				if end-2 >= start {
					follower[end-2] += 1.0
				}
			}
		}

		// Clamp follower and compute offsets/boost
		totBoost := 0
		for i := start; i < end; i++ {
			follower[i] = math.Min(follower[i], 4.0)

			// Scale down follower for offset computation
			// In libopus: follower[i] = SHR32(follower[i], 8)
			// Since we're in float, this is approximately dividing by 256
			// But actually libopus shifts by 8 bits in DB_SHIFT domain (which is 15 for dB values)
			// In float domain, we just keep the dB value and scale appropriately
			followerScaled := follower[i] / 256.0

			// Compute band width
			width := channels * ScaledBandWidth(i, 120<<lm) // 120 is base frame size
			if width <= 0 {
				width = 1
			}

			var boost, boostBits int

			// Different scaling based on band width
			// Reference: libopus lines 1242-1252
			if width < 6 {
				boost = int(followerScaled * 256) // Scale to match libopus fixed-point
				boostBits = boost * width << bitRes
			} else if width > 48 {
				boost = int(followerScaled * 256 * 8)
				boostBits = (boost * width << bitRes) / 8
			} else {
				boost = int(followerScaled * 256 * float64(width) / 6.0)
				boostBits = boost * 6 << bitRes
			}

			// For CBR and non-transient CVBR, limit dynalloc to 2/3 of bits
			if (!vbr || (constrainedVBR && !isTransient)) &&
				(totBoost+boostBits)>>bitRes>>3 > 2*effectiveBytes/3 {
				cap := (2 * effectiveBytes / 3) << bitRes << 3
				result.Offsets[i] = cap - totBoost
				totBoost = cap
				break
			} else {
				result.Offsets[i] = boost
				totBoost += boostBits
			}
		}
		result.TotBoost = totBoost
	} else {
		// Not enough bits for dynalloc, set uniform importance
		for i := start; i < end; i++ {
			result.Importance[i] = 13
		}
	}

	return result
}

// DynallocAnalysisSimple is a convenience wrapper for common mono encoding scenarios.
// It uses default parameters appropriate for typical audio encoding.
//
// Parameters:
//   - bandLogE: current frame band energies (log2 domain)
//   - nbBands: number of frequency bands
//   - lm: log2 of frame size multiplier
//   - effectiveBytes: total available bytes
//
// Returns: DynallocResult with maxDepth suitable for VBR floor_depth calculation
func DynallocAnalysisSimple(bandLogE []float64, nbBands, lm, effectiveBytes int) DynallocResult {
	// Use bandLogE as both primary and secondary energies (no separate transient MDCT)
	// Use default 16-bit depth and mono
	logN := make([]int16, nbBands)
	for i := 0; i < nbBands && i < len(LogN); i++ {
		logN[i] = int16(LogN[i])
	}

	return DynallocAnalysis(
		bandLogE, bandLogE, nil, // bandLogE2 = bandLogE, no oldBandE
		nbBands, 0, nbBands, 1, 16, lm,
		logN,
		effectiveBytes,
		false, true, false, // not transient, VBR, not constrained
		-1.0, 0.0,
	)
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
