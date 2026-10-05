package testvectors

import (
	"math"

	"github.com/thesyncim/gopus/internal/opusmath"
)

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

// float32ToPCM16 quantizes float32 PCM to int16 using libopus's float-to-int16
// rounding (mirrors the conversion the canonical comparator applies internally).
func float32ToPCM16(samples []float32) []int16 {
	out := make([]int16, len(samples))
	for i, s := range samples {
		out[i] = opusmath.Float32ToInt16(s)
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
	n := min(len(reference), len(decoded))
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

type waveformEnergyBounds struct {
	lower []float64
	upper []float64
}

func makeWaveformEnergyBounds(samples []float32) (waveformEnergyBounds, bool) {
	bounds := waveformEnergyBounds{
		lower: make([]float64, len(samples)+1),
		upper: make([]float64, len(samples)+1),
	}
	for i, sample := range samples {
		value := float64(sample)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return waveformEnergyBounds{}, false
		}
		square := value * value // Every float32 square is exact in float64.
		lower := math.Nextafter(bounds.lower[i]+square, math.Inf(-1))
		if lower < 0 {
			lower = 0
		}
		upper := math.Nextafter(bounds.upper[i]+square, math.Inf(1))
		if math.IsNaN(upper) {
			return waveformEnergyBounds{}, false
		}
		bounds.lower[i+1] = lower
		bounds.upper[i+1] = upper
	}
	return bounds, true
}

func (bounds waveformEnergyBounds) rangeUpper(start, end int) (float64, bool) {
	if start < 0 || end < start || end >= len(bounds.upper) || start >= len(bounds.lower) {
		return 0, false
	}
	if start == end {
		return 0, true
	}
	value := bounds.upper[end] - bounds.lower[start]
	if math.IsNaN(value) || value < 0 {
		return 0, false
	}
	return math.Nextafter(value, math.Inf(1)), true
}

func (bounds waveformEnergyBounds) rangeLower(start, end int) (float64, bool) {
	if start < 0 || end < start || end >= len(bounds.lower) || start >= len(bounds.upper) {
		return 0, false
	}
	if start == end {
		return 0, true
	}
	value := bounds.lower[end] - bounds.upper[start]
	if math.IsNaN(value) {
		return 0, false
	}
	if value <= 0 {
		return 0, true
	}
	value = math.Nextafter(value, math.Inf(-1))
	if value < 0 {
		value = 0
	}
	return value, true
}

func waveformAccumulatedEnergyLower(exactEnergyLower float64, terms int) (float64, bool) {
	if exactEnergyLower <= 0 || terms <= 0 {
		return 0, true
	}
	termsUpper := math.Nextafter(float64(terms), math.Inf(1))
	nuUpper := math.Nextafter(termsUpper*(1.0/9007199254740992.0), math.Inf(1))
	if nuUpper >= 0.5 {
		return 0, false
	}
	gammaUpper := math.Nextafter(2*nuUpper, math.Inf(1))
	factorLower := math.Nextafter(1-gammaUpper, math.Inf(-1))
	if factorLower <= 0 {
		return 0, false
	}
	lower := math.Nextafter(exactEnergyLower*factorLower, math.Inf(-1))
	if lower < 0 {
		lower = 0
	}
	return lower, true
}

func waveformCorrelationUpperBound(dot, refPower, decPower float64, remaining int, refTailEnergy, decTailEnergy float64) (float64, bool) {
	if remaining <= 0 || refPower <= 0 || decPower <= 0 || refTailEnergy < 0 || decTailEnergy < 0 {
		return 0, false
	}
	if math.IsNaN(dot) || math.IsInf(dot, 0) || math.IsNaN(refPower) || math.IsInf(refPower, 0) || math.IsNaN(decPower) || math.IsInf(decPower, 0) {
		return 0, false
	}

	productLower := math.Nextafter(refPower*decPower, math.Inf(-1))
	if productLower <= 0 || math.IsNaN(productLower) || math.IsInf(productLower, 0) {
		return 0, false
	}
	denominatorLower := math.Sqrt(productLower)
	if denominatorLower <= 0 || math.IsNaN(denominatorLower) || math.IsInf(denominatorLower, 0) {
		return 0, false
	}

	tailProductUpper := math.Nextafter(refTailEnergy*decTailEnergy, math.Inf(1))
	tailDotUpper := math.Nextafter(math.Sqrt(tailProductUpper), math.Inf(1))
	if math.IsNaN(tailDotUpper) {
		return 0, false
	}

	// For q remaining rounded additions, gamma_q <= 2*q*u when q*u < 1/2.
	// Larger inputs disable pruning below, so the inequality stays conservative.
	qUpper := math.Nextafter(float64(remaining), math.Inf(1))
	nuUpper := math.Nextafter(qUpper*(1.0/9007199254740992.0), math.Inf(1))
	if nuUpper >= 0.5 {
		return 0, false
	}
	gammaUpper := math.Nextafter(2*nuUpper, math.Inf(1))
	absoluteSumUpper := addWaveformUpper(math.Abs(dot), tailDotUpper)
	roundoffUpper := multiplyWaveformUpper(gammaUpper, absoluteSumUpper)
	dotUpper := addWaveformUpper(addWaveformUpper(dot, tailDotUpper), roundoffUpper)
	if math.IsNaN(dotUpper) {
		return 0, false
	}
	if dotUpper <= 0 {
		return 0, true
	}
	return math.Nextafter(dotUpper/denominatorLower, math.Inf(1)), true
}

func addWaveformUpper(a, b float64) float64 {
	value := a + b
	if math.IsNaN(value) {
		return math.Inf(1)
	}
	return math.Nextafter(value, math.Inf(1))
}

func multiplyWaveformUpper(a, b float64) float64 {
	value := a * b
	if math.IsNaN(value) {
		return math.Inf(1)
	}
	return math.Nextafter(value, math.Inf(1))
}

// Every delay remains eligible in scan order. Conservative bounds include
// rounded accumulation error and stop only proven losers; winners use the
// original full-stat calculation.
func bestWaveformDelayByCorrelation(decoded, reference []float32, maxDelay int) (int, waveformStats) {
	bestDelay := 0
	bestStats := computeWaveformStats(decoded, reference)
	if maxDelay < 0 {
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
	if maxDelay == 0 {
		return bestDelay, bestStats
	}
	decodedBounds, decodedBoundsOK := makeWaveformEnergyBounds(decoded)
	referenceBounds, referenceBoundsOK := makeWaveformEnergyBounds(reference)
	useBounds := decodedBoundsOK && referenceBoundsOK
	for delay := -maxDelay; delay <= maxDelay; delay++ {
		if delay == 0 {
			continue
		}
		alignedDecoded, alignedReference := alignFloat32ForDelay(decoded, reference, delay)
		n := min(len(alignedDecoded), len(alignedReference))
		if n == 0 {
			continue
		}

		decodedStart := 0
		referenceStart := 0
		if delay > 0 {
			decodedStart = delay
		} else {
			referenceStart = -delay
		}
		refFullEnergy, refFullOK := referenceBounds.rangeLower(referenceStart, referenceStart+n)
		decFullEnergy, decFullOK := decodedBounds.rangeLower(decodedStart, decodedStart+n)
		refFullPowerLower, refPowerLowerOK := waveformAccumulatedEnergyLower(refFullEnergy, n)
		decFullPowerLower, decPowerLowerOK := waveformAccumulatedEnergyLower(decFullEnergy, n)
		useCandidateBounds := useBounds && refFullOK && decFullOK && refPowerLowerOK && decPowerLowerOK
		var dot, refPower, decPower float64
		pruned := false
		for i := 0; i < n; i++ {
			ref := float64(alignedReference[i])
			dec := float64(alignedDecoded[i])
			dot += ref * dec
			refPower += ref * ref
			decPower += dec * dec
			processed := i + 1
			if useCandidateBounds && processed%1024 == 0 && processed < n && refPower > 0 && decPower > 0 {
				refTail, refOK := referenceBounds.rangeUpper(referenceStart+processed, referenceStart+n)
				decTail, decOK := decodedBounds.rangeUpper(decodedStart+processed, decodedStart+n)
				if refOK && decOK {
					refLower := math.Max(refPower, refFullPowerLower)
					decLower := math.Max(decPower, decFullPowerLower)
					upper, upperOK := waveformCorrelationUpperBound(dot, refLower, decLower, n-processed, refTail, decTail)
					if upperOK && upper < bestStats.Correlation {
						pruned = true
						break
					}
				}
			}
		}
		if pruned {
			continue
		}
		var correlation float64
		switch {
		case refPower == 0 && decPower == 0:
			correlation = 1
		case refPower == 0 || decPower == 0:
			correlation = 0
		default:
			correlation = dot / math.Sqrt(refPower*decPower)
		}
		if correlation > bestStats.Correlation || (correlation == bestStats.Correlation && qualityAbsInt(delay) < qualityAbsInt(bestDelay)) {
			bestDelay = delay
			alignedDecoded, alignedReference = alignFloat32ForDelay(decoded, reference, bestDelay)
			bestStats = computeWaveformStats(alignedDecoded, alignedReference)
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
