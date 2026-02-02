package silk

import "math"

// classifyFrame determines the signal type for a PCM frame.
// Returns signalType (0=inactive, 1=unvoiced, 2=voiced) and quantOffset (0=low, 1=high).
// Per RFC 6716 Section 4.2.7.3, draft-vos-silk-01 Section 2.1.2.2.
func (e *Encoder) classifyFrame(pcm []float32) (signalType, quantOffset int) {
	if len(pcm) == 0 {
		return 0, 0 // Inactive for empty input
	}

	// Compute frame energy
	var energy float64
	for _, s := range pcm {
		energy += float64(s) * float64(s)
	}
	energy /= float64(len(pcm))
	rmsEnergy := math.Sqrt(energy)

	// Inactive threshold: very low energy (normalized PCM)
	const inactiveThreshold = 1e-4 // Empirical, tune as needed
	if rmsEnergy < inactiveThreshold {
		return 0, 0 // Inactive
	}

	// Compute normalized autocorrelation at lag 1 (short-term correlation)
	var corr, norm float64
	for i := 1; i < len(pcm); i++ {
		corr += float64(pcm[i]) * float64(pcm[i-1])
		norm += float64(pcm[i-1]) * float64(pcm[i-1])
	}

	shortTermCorr := 0.0
	if norm > 0 {
		shortTermCorr = corr / norm
	}

	// Compute long-term periodicity (pitch-like correlation)
	// Search for peaks in autocorrelation at pitch lags
	config := GetBandwidthConfig(e.bandwidth)
	periodicity := e.computePeriodicity(pcm, config.PitchLagMin, config.PitchLagMax)

	// Classification logic per draft-vos-silk-01 (tuned for tonal signals)
	const voicedThreshold = 0.4         // Normalized periodicity threshold
	const voicedShortCorrThreshold = 0.8 // Short-term correlation fallback
	const highQuantThreshold = 0.6      // For quantization offset

	if periodicity > voicedThreshold || shortTermCorr > voicedShortCorrThreshold {
		signalType = 2 // Voiced
	} else {
		signalType = 1 // Unvoiced
	}

	// Quantization offset based on energy and periodicity
	if periodicity > highQuantThreshold || shortTermCorr > 0.85 {
		quantOffset = 1 // High
	} else {
		quantOffset = 0 // Low
	}

	return signalType, quantOffset
}

// computePeriodicity computes normalized autocorrelation in pitch range.
// Returns max normalized correlation (0 to 1, higher = more periodic/voiced).
func (e *Encoder) computePeriodicity(pcm []float32, minLag, maxLag int) float64 {
	n := len(pcm)
	if maxLag >= n {
		maxLag = n - 1
	}
	if minLag < 1 {
		minLag = 1
	}
	if minLag > maxLag {
		return 0
	}

	var maxCorr float64 = 0

	for lag := minLag; lag <= maxLag; lag++ {
		var corr, energy1, energy2 float64
		for i := lag; i < n; i++ {
			corr += float64(pcm[i]) * float64(pcm[i-lag])
			energy1 += float64(pcm[i]) * float64(pcm[i])
			energy2 += float64(pcm[i-lag]) * float64(pcm[i-lag])
		}

		if energy1 > 0 && energy2 > 0 {
			normCorr := corr / math.Sqrt(energy1*energy2)
			if normCorr > maxCorr {
				maxCorr = normCorr
			}
		}
	}

	return maxCorr
}
