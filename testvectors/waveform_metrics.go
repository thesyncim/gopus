package testvectors

import "math"

type waveformStats struct {
	Samples     int
	Correlation float64
	RMSRatio    float64
	MeanAbsErr  float64
	RMSErr      float64
	MaxAbsErr   float64
}

func pcm16ToFloat32(samples []int16) []float32 {
	out := make([]float32, len(samples))
	for i, sample := range samples {
		out[i] = float32(sample) / 32768.0
	}
	return out
}

func alignFloat32ForDelay(decoded, reference []float32, delay int) ([]float32, []float32) {
	refStart := 0
	decStart := 0
	if delay > 0 {
		decStart = delay
	} else if delay < 0 {
		refStart = -delay
	}
	if refStart >= len(reference) || decStart >= len(decoded) {
		return nil, nil
	}

	n := len(reference) - refStart
	if rem := len(decoded) - decStart; rem < n {
		n = rem
	}
	if n <= 0 {
		return nil, nil
	}

	return decoded[decStart : decStart+n], reference[refStart : refStart+n]
}

func computeWaveformStats(decoded, reference []float32) waveformStats {
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}
	if n == 0 {
		return waveformStats{}
	}

	var dot float64
	var refPower float64
	var decPower float64
	var absErr float64
	var sqErr float64
	var maxAbsErr float64
	for i := 0; i < n; i++ {
		ref := float64(reference[i])
		dec := float64(decoded[i])
		diff := dec - ref
		absDiff := math.Abs(diff)

		dot += ref * dec
		refPower += ref * ref
		decPower += dec * dec
		absErr += absDiff
		sqErr += diff * diff
		if absDiff > maxAbsErr {
			maxAbsErr = absDiff
		}
	}

	stats := waveformStats{
		Samples:    n,
		MeanAbsErr: absErr / float64(n),
		RMSErr:     math.Sqrt(sqErr / float64(n)),
		MaxAbsErr:  maxAbsErr,
	}

	switch {
	case refPower == 0 && decPower == 0:
		stats.Correlation = 1.0
		stats.RMSRatio = 1.0
	case refPower == 0 || decPower == 0:
		stats.Correlation = 0.0
		stats.RMSRatio = 0.0
	default:
		stats.Correlation = dot / math.Sqrt(refPower*decPower)
		stats.RMSRatio = math.Sqrt(decPower / refPower)
	}

	return stats
}

func bestWaveformDelayByCorrelation(decoded, reference []float32, maxDelay int) (int, waveformStats) {
	bestDelay := 0
	bestStats := computeWaveformStats(decoded, reference)
	for delay := -maxDelay; delay <= maxDelay; delay++ {
		alignedDecoded, alignedReference := alignFloat32ForDelay(decoded, reference, delay)
		stats := computeWaveformStats(alignedDecoded, alignedReference)
		if stats.Samples == 0 {
			continue
		}
		if stats.Correlation > bestStats.Correlation || (stats.Correlation == bestStats.Correlation && qualityAbsInt(delay) < qualityAbsInt(bestDelay)) {
			bestDelay = delay
			bestStats = stats
		}
	}
	return bestDelay, bestStats
}

func qualityAbsInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
