// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides tonality analysis for VBR bit allocation decisions.

package celt

import "math"

// TonalityAnalysisResult holds the results of tonality analysis.
// Tonality measures how "tonal" (pitched/harmonic) vs "noisy" (aperiodic) a signal is.
// This information is used by the VBR algorithm to allocate more bits to tonal signals
// which benefit more from accurate spectral representation.
type TonalityAnalysisResult struct {
	Tonality     float64   // Overall tonality (0=noise, 1=pure tone)
	SFM          float64   // Spectral Flatness Measure (0=tonal, 1=flat/noise)
	BandTonality []float64 // Per-band tonality estimates
	SpectralFlux float64   // Frame-to-frame spectral change (0=stationary, higher=transient)
}

// ComputeTonality analyzes MDCT coefficients to estimate signal tonality.
// Uses the Spectral Flatness Measure (SFM) which compares geometric mean to arithmetic mean.
// A flat spectrum (noise) has SFM close to 1, while a peaked spectrum (tone) has SFM close to 0.
// Tonality is computed as 1 - SFM, so tones have high tonality.
//
// This is a variadic function that supports two call patterns:
// - ComputeTonality(mdctCoeffs, nbBands, frameSize) - explicit band configuration
// - ComputeTonality(mdctCoeffs, prevCoeffs) - with previous frame for flux (legacy)
//
// Parameters:
//   - mdctCoeffs: MDCT coefficients for one channel
//   - args: either (nbBands int, frameSize int) or (prevCoeffs []float64)
//
// Returns: TonalityAnalysisResult with overall and per-band tonality
//
// Reference: ITU-R BS.1387 (PEAQ) for SFM definition
func ComputeTonality(mdctCoeffs []float64, args ...interface{}) TonalityAnalysisResult {
	// Handle legacy 3-argument form: (coeffs, nbBands, frameSize)
	if len(args) == 2 {
		if nbBands, ok1 := args[0].(int); ok1 {
			if frameSize, ok2 := args[1].(int); ok2 {
				return ComputeTonalityWithBands(mdctCoeffs, nbBands, frameSize)
			}
		}
	}

	// Handle 2-argument form with previous coefficients: (coeffs, prevCoeffs)
	var prevCoeffs []float64
	if len(args) >= 1 {
		if prev, ok := args[0].([]float64); ok {
			prevCoeffs = prev
		}
	}

	return computeTonalityInternal(mdctCoeffs, prevCoeffs)
}

// computeTonalityInternal is the internal implementation that takes explicit prevCoeffs.
func computeTonalityInternal(mdctCoeffs, prevCoeffs []float64) TonalityAnalysisResult {
	result := TonalityAnalysisResult{
		Tonality:     0.5, // Default to mid-range if computation fails
		SFM:          0.5, // Default SFM
		BandTonality: nil,
		SpectralFlux: 0.0,
	}

	if len(mdctCoeffs) == 0 {
		return result
	}

	// Compute power spectrum
	powers := make([]float64, len(mdctCoeffs))
	for i, coeff := range mdctCoeffs {
		powers[i] = coeff * coeff
	}

	// Compute overall SFM
	result.SFM = computeSpectralFlatness(powers)

	// Tonality is inverse of SFM: peaked spectrum -> high tonality
	result.Tonality = 1.0 - result.SFM

	// Clamp to valid range
	if result.Tonality < 0 {
		result.Tonality = 0
	}
	if result.Tonality > 1 {
		result.Tonality = 1
	}

	// Compute per-band tonality using CELT band structure
	// Infer frame size from coefficient count (frameSize = len(coeffs))
	frameSize := len(mdctCoeffs)
	nbBands := inferBandCount(frameSize)

	if nbBands > 0 {
		result.BandTonality = computePerBandTonality(mdctCoeffs, nbBands, frameSize)
	}

	// Compute spectral flux if previous coefficients are available
	if len(prevCoeffs) > 0 && len(prevCoeffs) == len(mdctCoeffs) {
		result.SpectralFlux = computeSpectralFluxFromCoeffs(mdctCoeffs, prevCoeffs)
	}

	return result
}

// ComputeTonalityWithBands analyzes MDCT coefficients with explicit band count.
// This is the more precise version that takes explicit nbBands and frameSize.
//
// Parameters:
//   - mdctCoeffs: MDCT coefficients for one channel
//   - nbBands: number of frequency bands to analyze
//   - frameSize: frame size in samples (used to scale band boundaries)
//
// Returns: TonalityAnalysisResult with overall and per-band tonality
func ComputeTonalityWithBands(mdctCoeffs []float64, nbBands, frameSize int) TonalityAnalysisResult {
	result := TonalityAnalysisResult{
		Tonality:     0.5, // Default to mid-range if computation fails
		SFM:          0.5,
		BandTonality: make([]float64, nbBands),
		SpectralFlux: 0.0,
	}

	if len(mdctCoeffs) == 0 || nbBands <= 0 || frameSize <= 0 {
		return result
	}

	// Compute power spectrum
	powers := make([]float64, len(mdctCoeffs))
	for i, coeff := range mdctCoeffs {
		powers[i] = coeff * coeff
	}

	// Compute overall SFM
	result.SFM = computeSpectralFlatness(powers)
	result.Tonality = 1.0 - result.SFM

	// Clamp to valid range
	if result.Tonality < 0 {
		result.Tonality = 0
	}
	if result.Tonality > 1 {
		result.Tonality = 1
	}

	// Compute per-band tonality
	result.BandTonality = computePerBandTonality(mdctCoeffs, nbBands, frameSize)

	return result
}

// computePerBandTonality computes tonality for each CELT band.
func computePerBandTonality(mdctCoeffs []float64, nbBands, frameSize int) []float64 {
	bandTonality := make([]float64, nbBands)

	scale := frameSize / Overlap // 1 for 2.5ms, 2 for 5ms, 4 for 10ms, 8 for 20ms
	if scale < 1 {
		scale = 1
	}

	for band := 0; band < nbBands; band++ {
		if band+1 >= len(EBands) {
			break
		}

		startBin := EBands[band] * scale
		endBin := EBands[band+1] * scale

		if startBin >= len(mdctCoeffs) {
			bandTonality[band] = 0.5
			continue
		}
		if endBin > len(mdctCoeffs) {
			endBin = len(mdctCoeffs)
		}

		bandWidth := endBin - startBin
		if bandWidth <= 0 {
			bandTonality[band] = 0.5
			continue
		}

		// Compute power spectrum for this band
		powers := make([]float64, bandWidth)
		for i := 0; i < bandWidth; i++ {
			idx := startBin + i
			if idx < len(mdctCoeffs) {
				powers[i] = mdctCoeffs[idx] * mdctCoeffs[idx]
			}
		}

		// Compute SFM for this band
		sfm := computeSpectralFlatness(powers)

		// Convert SFM to tonality
		bt := 1.0 - sfm
		if bt < 0 {
			bt = 0
		}
		if bt > 1 {
			bt = 1
		}
		bandTonality[band] = bt
	}

	return bandTonality
}

// TonalityScratch holds pre-allocated buffers for tonality analysis.
// This eliminates allocations in the hot path.
type TonalityScratch struct {
	Powers       []float64 // Power spectrum buffer (size: frameSize)
	BandTonality []float64 // Per-band tonality output (size: nbBands)
	BandPowers   []float64 // Temporary buffer for per-band power (size: max band width ~176)
}

// EnsureTonalityScratch ensures the scratch buffers are large enough.
func (s *TonalityScratch) EnsureTonalityScratch(frameSize, nbBands int) {
	if cap(s.Powers) < frameSize {
		s.Powers = make([]float64, frameSize)
	} else {
		s.Powers = s.Powers[:frameSize]
	}
	if cap(s.BandTonality) < nbBands {
		s.BandTonality = make([]float64, nbBands)
	} else {
		s.BandTonality = s.BandTonality[:nbBands]
	}
	// Max band width is ~176 bins (band 20 at LM=3)
	const maxBandWidth = 256
	if cap(s.BandPowers) < maxBandWidth {
		s.BandPowers = make([]float64, maxBandWidth)
	}
}

// ComputeTonalityWithBandsScratch analyzes MDCT coefficients with explicit band count using pre-allocated scratch buffers.
// This is the zero-allocation version.
func ComputeTonalityWithBandsScratch(mdctCoeffs []float64, nbBands, frameSize int, scratch *TonalityScratch) TonalityAnalysisResult {
	result := TonalityAnalysisResult{
		Tonality:     0.5,
		SFM:          0.5,
		BandTonality: nil,
		SpectralFlux: 0.0,
	}

	if len(mdctCoeffs) == 0 || nbBands <= 0 || frameSize <= 0 || scratch == nil {
		return result
	}

	// Ensure scratch buffers are sized
	scratch.EnsureTonalityScratch(len(mdctCoeffs), nbBands)

	// Compute power spectrum into scratch buffer
	powers := scratch.Powers[:len(mdctCoeffs)]
	for i, coeff := range mdctCoeffs {
		powers[i] = coeff * coeff
	}

	// Compute overall SFM
	result.SFM = computeSpectralFlatness(powers)
	result.Tonality = 1.0 - result.SFM

	// Clamp to valid range
	if result.Tonality < 0 {
		result.Tonality = 0
	}
	if result.Tonality > 1 {
		result.Tonality = 1
	}

	// Compute per-band tonality using scratch
	computePerBandTonalityScratch(mdctCoeffs, nbBands, frameSize, scratch)
	result.BandTonality = scratch.BandTonality[:nbBands]

	return result
}

// computePerBandTonalityScratch computes tonality for each CELT band using pre-allocated scratch buffers.
func computePerBandTonalityScratch(mdctCoeffs []float64, nbBands, frameSize int, scratch *TonalityScratch) {
	bandTonality := scratch.BandTonality[:nbBands]

	scale := frameSize / Overlap
	if scale < 1 {
		scale = 1
	}

	for band := 0; band < nbBands; band++ {
		if band+1 >= len(EBands) {
			break
		}

		startBin := EBands[band] * scale
		endBin := EBands[band+1] * scale

		if startBin >= len(mdctCoeffs) {
			bandTonality[band] = 0.5
			continue
		}
		if endBin > len(mdctCoeffs) {
			endBin = len(mdctCoeffs)
		}

		bandWidth := endBin - startBin
		if bandWidth <= 0 {
			bandTonality[band] = 0.5
			continue
		}

		// Use scratch buffer for band powers
		powers := scratch.BandPowers[:bandWidth]
		for i := 0; i < bandWidth; i++ {
			idx := startBin + i
			if idx < len(mdctCoeffs) {
				powers[i] = mdctCoeffs[idx] * mdctCoeffs[idx]
			}
		}

		// Compute SFM for this band
		sfm := computeSpectralFlatness(powers)

		// Convert SFM to tonality
		bt := 1.0 - sfm
		if bt < 0 {
			bt = 0
		}
		if bt > 1 {
			bt = 1
		}
		bandTonality[band] = bt
	}
}

// inferBandCount infers the number of bands from frame size.
func inferBandCount(frameSize int) int {
	// Standard CELT band count is 21 for all frame sizes
	// But effective bands depend on sample rate and frame size
	switch frameSize {
	case 120:
		return 21 // 2.5ms at 48kHz
	case 240:
		return 21 // 5ms at 48kHz
	case 480:
		return 21 // 10ms at 48kHz
	case 960:
		return 21 // 20ms at 48kHz
	default:
		// For non-standard sizes, estimate based on typical CELT configuration
		if frameSize > 0 {
			return 21
		}
		return 0
	}
}

// computeSpectralFluxFromCoeffs computes spectral flux from MDCT coefficients.
func computeSpectralFluxFromCoeffs(current, previous []float64) float64 {
	if len(current) == 0 || len(previous) == 0 {
		return 0.0
	}

	n := len(current)
	if len(previous) < n {
		n = len(previous)
	}

	var flux float64
	const epsilon = 1e-20

	for i := 0; i < n; i++ {
		// Compute power for each bin
		currPow := current[i]*current[i] + epsilon
		prevPow := previous[i]*previous[i] + epsilon

		// Log-domain difference (perceptually relevant)
		diff := math.Log(currPow) - math.Log(prevPow)
		flux += diff * diff
	}

	// Normalize by number of bins
	flux = flux / float64(n)

	// Apply soft saturation to map to [0, 1] range
	const fluxScale = 4.0
	normalizedFlux := 1.0 - math.Exp(-flux/fluxScale)

	return normalizedFlux
}

// ComputeSpectralFlux computes the frame-to-frame spectral change.
// This measures how much the spectrum has changed between consecutive frames.
// Low flux indicates a stationary tone, high flux indicates transients or noise.
//
// Parameters:
//   - currentEnergies: current frame band energies (log-domain)
//   - previousEnergies: previous frame band energies (log-domain)
//   - nbBands: number of bands to compare
//
// Returns: normalized spectral flux in range [0, 1]
//
// Reference: libopus uses similar metrics for transient detection
func ComputeSpectralFlux(currentEnergies, previousEnergies []float64, nbBands int) float64 {
	if len(currentEnergies) == 0 || len(previousEnergies) == 0 || nbBands <= 0 {
		return 0.0
	}

	var flux float64
	var count int

	// Sum of squared differences in log-energy
	for i := 0; i < nbBands; i++ {
		if i >= len(currentEnergies) || i >= len(previousEnergies) {
			break
		}

		// Use log energies for perceptual relevance
		// Add small epsilon to avoid log(0)
		const epsilon = 1e-10
		currentLog := safeLog(currentEnergies[i] + epsilon)
		prevLog := safeLog(previousEnergies[i] + epsilon)

		diff := currentLog - prevLog
		flux += diff * diff
		count++
	}

	if count == 0 {
		return 0.0
	}

	// Normalize by number of bands
	flux = flux / float64(count)

	// Apply soft saturation to map to [0, 1] range
	const fluxScale = 4.0
	normalizedFlux := 1.0 - math.Exp(-flux/fluxScale)

	return normalizedFlux
}

// computeSpectralFlatness computes the Spectral Flatness Measure (SFM).
// SFM = geometric_mean(|X|^2) / arithmetic_mean(|X|^2)
// For computational stability, this is computed as:
// SFM = exp(mean(log(|X|^2))) / mean(|X|^2)
//
// Returns value in [0, 1] where 1 = perfectly flat (noise), 0 = perfectly peaked (tone)
func computeSpectralFlatness(powers []float64) float64 {
	if len(powers) == 0 {
		return 1.0 // Default to flat (noise) for empty input
	}

	geoMean := geometricMean(powers)
	arithMean := arithmeticMean(powers)

	if arithMean <= 0 {
		return 1.0 // Flat spectrum for zero/near-zero power
	}

	sfm := geoMean / arithMean

	// Clamp to valid range (numerical errors can push slightly outside)
	if sfm < 0 {
		sfm = 0
	}
	if sfm > 1 {
		sfm = 1
	}

	return sfm
}

// geometricMean computes the geometric mean of positive values.
// Uses exp(mean(log(x))) for numerical stability.
// Handles zero/negative values by clamping to a small epsilon.
func geometricMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	const epsilon = 1e-20 // Minimum value to avoid log(0)

	var sumLog float64
	for _, v := range values {
		// Clamp to minimum positive value
		if v < epsilon {
			v = epsilon
		}
		sumLog += math.Log(v)
	}

	meanLog := sumLog / float64(len(values))
	return math.Exp(meanLog)
}

// arithmeticMean computes the arithmetic mean of values.
func arithmeticMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	var sum float64
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}

// safeLog computes natural logarithm with protection against negative/zero values.
func safeLog(x float64) float64 {
	const epsilon = 1e-20
	if x < epsilon {
		x = epsilon
	}
	return math.Log(x)
}

// computePerceptualWeights returns per-band weights based on perceptual importance.
// Lower frequency bands (where speech fundamentals and music harmonics live) are weighted higher.
// This matches libopus's emphasis on perceptually important frequency ranges.
func computePerceptualWeights(nbBands int) []float64 {
	weights := make([]float64, nbBands)

	for i := 0; i < nbBands; i++ {
		// Weights decrease logarithmically with band index
		// Band 0-7 (0-1600 Hz): full weight (speech fundamentals, music fundamentals)
		// Band 8-13 (1600-4000 Hz): moderate weight (speech formants, harmonics)
		// Band 14-20 (4000-20000 Hz): lower weight (high harmonics, less critical)
		if i < 8 {
			weights[i] = 1.0
		} else if i < 14 {
			weights[i] = 0.7
		} else {
			weights[i] = 0.3
		}
	}

	return weights
}

// ComputeTonalityFromNormalized computes tonality from pre-normalized MDCT coefficients.
// This is useful when normalization has already been done (as in encode_frame.go).
//
// Parameters:
//   - normCoeffs: normalized MDCT coefficients (unit energy per band)
//   - nbBands: number of frequency bands
//   - frameSize: frame size for scaling band boundaries
//
// Returns: TonalityAnalysisResult
func ComputeTonalityFromNormalized(normCoeffs []float64, nbBands, frameSize int) TonalityAnalysisResult {
	// For normalized coefficients, we need to analyze the distribution within bands
	// rather than absolute magnitudes
	return ComputeTonalityWithBands(normCoeffs, nbBands, frameSize)
}
