// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides allocation trim analysis for optimal bit allocation.

package celt

import "math"

// AllocTrimAnalysis computes the optimal allocation trim value for a CELT frame.
// The trim value biases bit allocation between lower and higher frequency bands.
// A higher trim value allocates more bits to lower frequencies.
//
// The algorithm considers:
// - Equivalent bitrate (lower bitrates favor lower trim)
// - Spectral tilt (energy distribution across bands)
// - TF estimate (transient characteristic)
// - Stereo correlation (for stereo signals)
// - Tonality slope (optional, from analysis)
//
// Parameters:
//   - normCoeffs: normalized MDCT coefficients (left channel for stereo, or mono)
//   - bandLogE: band log-energies [nbBands * channels]
//   - nbBands: number of frequency bands
//   - lm: log mode (frame size index)
//   - channels: 1 for mono, 2 for stereo
//   - normCoeffsRight: normalized right channel coefficients (nil for mono)
//   - intensity: intensity stereo band threshold (nbBands for no intensity stereo)
//   - tfEstimate: TF estimate from transient analysis (0.0-1.0)
//   - equivRate: equivalent bitrate in bits per second
//   - surroundTrim: surround mix trim adjustment (0 for non-surround)
//   - tonalitySlope: tonality slope from analysis (-1 to 1, 0 if not available)
//
// Returns: trim index in range [0, 10], where 5 is the neutral default
//
// Reference: libopus celt/celt_encoder.c alloc_trim_analysis()
func AllocTrimAnalysis(
	normCoeffs []float64,
	bandLogE []float64,
	nbBands int,
	lm int,
	channels int,
	normCoeffsRight []float64,
	intensity int,
	tfEstimate float64,
	equivRate int,
	surroundTrim float64,
	tonalitySlope float64,
) int {
	// Start with default trim of 5
	trim := 5.0

	// At low bitrate, reducing the trim seems to help. At higher bitrates, it's less
	// clear what's best, so we're keeping it as it was before, at least for now.
	// Reference: libopus lines 877-883
	if equivRate < 64000 {
		trim = 4.0
	} else if equivRate < 80000 {
		// Linear interpolation from 4.0 to 5.0 between 64kbps and 80kbps
		// libopus: trim = 4.f + (equiv_rate-64000)/16000
		frac := float64(equivRate-64000) / 16000.0
		trim = 4.0 + frac
	}

	// Stereo correlation adjustment
	// Reference: libopus lines 884-920
	if channels == 2 && normCoeffsRight != nil && len(normCoeffs) > 0 && len(normCoeffsRight) > 0 {
		logXC := computeStereoCorrelationTrim(normCoeffs, normCoeffsRight, nbBands, lm, intensity)

		// trim += max(-4, 0.75 * logXC)
		stereoAdjust := 0.75 * logXC
		if stereoAdjust < -4.0 {
			stereoAdjust = -4.0
		}
		trim += stereoAdjust
	}

	// Spectral tilt adjustment
	// Reference: libopus lines 922-931
	// The spectral tilt measures whether energy is concentrated in low or high frequencies.
	// Positive diff = more energy in lower bands (tilted down)
	// Negative diff = more energy in higher bands (tilted up)
	var diff float64
	end := nbBands
	if end > len(bandLogE)/channels {
		end = len(bandLogE) / channels
	}

	for c := 0; c < channels; c++ {
		for i := 0; i < end-1; i++ {
			idx := i + c*nbBands
			if idx < len(bandLogE) {
				// Weight each band by its position relative to center
				// Lower bands (small i) get negative weights, higher bands get positive weights
				// libopus: diff += (bandLogE[i+c*nbEBands] >> 5) * (2 + 2*i - end)
				// In float builds this is effectively division by 32.0.
				weight := float64(2 + 2*i - end)
				diff += (bandLogE[idx] / 32.0) * weight
			}
		}
	}
	diff /= float64(channels * (end - 1))

	// Apply spectral tilt adjustment, clamped to [-2, 2] range
	// libopus: trim -= max(-2, min(2, (diff+1)/6))
	// Note: In libopus, diff has DB_SHIFT scaling which we account for here.
	// Our bandLogE is in log2 units (~1.0 = 6dB), similar to libopus float mode.
	tiltAdjust := (diff + 1.0) / 6.0
	if tiltAdjust < -2.0 {
		tiltAdjust = -2.0
	}
	if tiltAdjust > 2.0 {
		tiltAdjust = 2.0
	}

	trim -= tiltAdjust

	// Surround trim adjustment
	// Reference: libopus line 932
	// surround_trim is in dB, typically 0 for non-surround encoding
	trim -= surroundTrim

	// TF estimate adjustment
	// Reference: libopus line 933: trim -= 2*SHR16(tf_estimate, 14-8)
	// tf_estimate is in Q14 format in libopus, we use float [0, 1]
	// So: trim -= 2 * tf_estimate
	trim -= 2.0 * tfEstimate

	// Tonality slope adjustment (optional, from analysis)
	// Reference: libopus lines 935-939
	// tonality_slope ranges from about -0.25 to 0.25 in practice
	// libopus: trim -= max(-2, min(2, 2*(tonality_slope + 0.05)))
	if tonalitySlope != 0 {
		tonalAdjust := 2.0 * (tonalitySlope + 0.05)
		if tonalAdjust < -2.0 {
			tonalAdjust = -2.0
		}
		if tonalAdjust > 2.0 {
			tonalAdjust = 2.0
		}
		trim -= tonalAdjust
	}

	// Convert to integer with rounding and clamp to valid range
	// Reference: libopus lines 947-949
	trimIndex := int(math.Floor(0.5 + trim))
	if trimIndex < 0 {
		trimIndex = 0
	}
	if trimIndex > 10 {
		trimIndex = 10
	}

	return trimIndex
}

// computeStereoCorrelationTrim computes the stereo correlation adjustment for alloc_trim.
// It measures inter-channel correlation to estimate mid-side coding savings.
//
// Reference: libopus celt/celt_encoder.c alloc_trim_analysis() lines 884-920
func computeStereoCorrelationTrim(normL, normR []float64, nbBands, lm, intensity int) float64 {
	// Compute inter-channel correlation for low frequencies (first 8 bands)
	// libopus uses inner product of normalized coefficients between channels

	var sum float64
	var count int

	// Compute correlation for first 8 bands
	for band := 0; band < 8 && band < nbBands; band++ {
		bandStart := EBands[band] << lm
		bandEnd := EBands[band+1] << lm

		if bandStart >= len(normL) || bandStart >= len(normR) {
			break
		}
		if bandEnd > len(normL) {
			bandEnd = len(normL)
		}
		if bandEnd > len(normR) {
			bandEnd = len(normR)
		}

		var partial float64
		for j := bandStart; j < bandEnd; j++ {
			partial += normL[j] * normR[j]
		}
		sum += partial
		count++
	}

	if count > 0 {
		sum /= float64(count)
	}

	// Clamp sum to [-1, 1]
	if sum > 1.0 {
		sum = 1.0
	}
	if sum < -1.0 {
		sum = -1.0
	}
	sum = math.Abs(sum)

	// Also compute minimum correlation across higher bands (up to intensity threshold)
	minXC := sum
	for band := 8; band < intensity && band < nbBands; band++ {
		bandStart := EBands[band] << lm
		bandEnd := EBands[band+1] << lm

		if bandStart >= len(normL) || bandStart >= len(normR) {
			break
		}
		if bandEnd > len(normL) {
			bandEnd = len(normL)
		}
		if bandEnd > len(normR) {
			bandEnd = len(normR)
		}

		var partial float64
		for j := bandStart; j < bandEnd; j++ {
			partial += normL[j] * normR[j]
		}
		partial = math.Abs(partial)

		if partial < minXC {
			minXC = partial
		}
	}

	// Compute log correlation: log2(1.001 - sum^2)
	// This gives a negative value; higher correlation = more negative
	logXC := math.Log2(1.001 - sum*sum)

	return logXC
}

// ComputeEquivRate computes the equivalent bitrate for allocation trim analysis.
// This matches libopus computation in celt_encoder.c line 1925.
//
// Parameters:
//   - nbCompressedBytes: target compressed packet size in bytes
//   - channels: number of audio channels (1 or 2)
//   - lm: log mode (frame size index: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms)
//   - targetBitrate: target bitrate in bps (0 if using fixed packet size)
//
// Returns: equivalent bitrate in bits per second
//
// Reference: libopus celt/celt_encoder.c line 1925:
//
//	equiv_rate = ((opus_int32)nbCompressedBytes*8*50 << (3-LM)) - (40*C+20)*((400>>LM) - 50);
func ComputeEquivRate(nbCompressedBytes, channels, lm, targetBitrate int) int {
	// Base computation from packet size
	// 50 is the frame rate for 20ms frames at 48kHz
	// (3-LM) scales for shorter frames
	equivRate := (nbCompressedBytes * 8 * 50) << (3 - lm)

	// Subtract overhead
	// (40*C+20) is the overhead per frame in bits (approx header size)
	// ((400>>LM) - 50) is the frame rate difference from base 50fps
	overhead := (40*channels + 20) * ((400 >> lm) - 50)
	equivRate -= overhead

	// If we have a target bitrate, take the minimum
	if targetBitrate > 0 {
		bitrateEquiv := targetBitrate - (40*channels+20)*((400>>lm)-50)
		if bitrateEquiv < equivRate {
			equivRate = bitrateEquiv
		}
	}

	return equivRate
}
