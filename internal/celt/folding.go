package celt

// Band folding for uncoded bands in CELT.
// When a band receives zero bit allocation (k=0 pulses), its shape is
// reconstructed by "folding" from a lower coded band with pseudo-random
// sign variations. This provides perceptually acceptable noise fill.
//
// Reference: RFC 6716 Section 4.3.4, libopus celt/bands.c

// FoldBand generates a normalized vector by folding from a lower band.
// lowband: the source band vector (already decoded and normalized)
// n: width of target band (number of MDCT bins)
// seed: RNG state for sign variation (modified in place)
// Returns: normalized vector of length n with unit L2 norm.
//
// If lowband is empty or nil, generates pseudo-random noise instead.
func FoldBand(lowband []float64, n int, seed *uint32) []float64 {
	if n <= 0 {
		return nil
	}

	result := make([]float64, n)

	if len(lowband) == 0 {
		// No source band available - generate pseudo-random noise
		// Uses LCG (Linear Congruential Generator) matching libopus
		for i := 0; i < n; i++ {
			*seed = *seed*1664525 + 1013904223 // LCG constants
			// Convert to signed float in approximately [-1, 1]
			result[i] = float64(int32(*seed)) / float64(1<<31)
		}
	} else {
		// Copy from lower band with pseudo-random sign flips
		// The sign is determined by bit 15 of the RNG state
		for i := 0; i < n; i++ {
			// Determine sign from current seed
			sign := 1.0
			if *seed&0x8000 != 0 {
				sign = -1.0
			}
			// Advance RNG
			*seed = *seed*1664525 + 1013904223

			// Copy from lowband with wrapping if target is larger
			result[i] = sign * lowband[i%len(lowband)]
		}
	}

	// Normalize to unit energy
	return NormalizeVector(result)
}

// FindFoldSource finds the band to fold from for an uncoded band.
// targetBand: the band index that needs folding
// codedMask: bitmask of bands that have been coded (received pulses)
// bandWidths: array of band widths (from eBands table)
// Returns: index of source band to fold from, or -1 if none available.
//
// The algorithm searches backwards from targetBand to find the most recent
// coded band that can serve as a reasonable source.
func FindFoldSource(targetBand int, codedMask uint32, bandWidths []int) int {
	if targetBand <= 0 || codedMask == 0 {
		return -1
	}

	// Search backwards for a coded band
	for b := targetBand - 1; b >= 0; b-- {
		if codedMask&(1<<b) != 0 {
			// Found a coded band
			return b
		}
	}

	return -1 // No coded band found
}

// FindFoldSourceWithOffset finds a source band and offset for folding.
// This variant returns additional offset information for more precise folding.
// targetBand: the band index that needs folding
// targetOffset: starting MDCT bin of target band
// codedBands: slice of decoded band vectors (indexed by band number)
// Returns: source band index, offset within source, and whether found.
//
// Reference: libopus celt/bands.c compute_band_fold()
func FindFoldSourceWithOffset(targetBand int, targetOffset int, codedBands [][]float64) (srcBand, offset int, found bool) {
	if targetBand <= 0 || len(codedBands) == 0 {
		return -1, 0, false
	}

	// Search backwards for a non-empty coded band
	for b := targetBand - 1; b >= 0; b-- {
		if len(codedBands[b]) > 0 {
			return b, 0, true
		}
	}

	return -1, 0, false
}

// UpdateCollapseMask marks a band as having received pulses.
// mask: pointer to collapse mask (modified in place)
// band: band index to mark as coded
//
// The collapse mask tracks which bands received non-zero bit allocation.
// This is used for anti-collapse processing in transient frames.
func UpdateCollapseMask(mask *uint32, band int) {
	if band < 0 || band >= 32 {
		return
	}
	*mask |= 1 << band
}

// NeedsAntiCollapse checks if a band collapsed (no pulses in transient frame).
// mask: collapse mask from current frame
// band: band index to check
// Returns: true if the band collapsed and needs anti-collapse noise injection.
//
// Reference: RFC 6716 Section 4.3.5
func NeedsAntiCollapse(mask uint32, band int) bool {
	if band < 0 || band >= 32 {
		return false
	}
	// Band collapsed if it's not in the coded mask
	return mask&(1<<band) == 0
}

// IsBandCoded checks if a band was coded (received pulses).
// mask: collapse mask
// band: band index
// Returns: true if the band received non-zero bit allocation.
func IsBandCoded(mask uint32, band int) bool {
	if band < 0 || band >= 32 {
		return false
	}
	return mask&(1<<band) != 0
}

// ClearCollapseMask resets the collapse mask to zero.
func ClearCollapseMask(mask *uint32) {
	*mask = 0
}

// GetCodedBandCount returns the number of bands with pulses.
// mask: collapse mask
// Returns: count of coded bands.
func GetCodedBandCount(mask uint32) int {
	count := 0
	for mask != 0 {
		count += int(mask & 1)
		mask >>= 1
	}
	return count
}

// FoldBandFromMultiple generates a folded vector using multiple source bands.
// This provides better spectral diversity for uncoded high-frequency bands.
// sources: slice of source band vectors to fold from
// n: width of target band
// seed: RNG state
// Returns: normalized vector of length n.
//
// Reference: libopus uses this approach for better quality folding.
func FoldBandFromMultiple(sources [][]float64, n int, seed *uint32) []float64 {
	if n <= 0 {
		return nil
	}

	if len(sources) == 0 {
		// No sources - fall back to noise
		return FoldBand(nil, n, seed)
	}

	result := make([]float64, n)

	// Combine multiple sources with sign variation
	sourceIdx := 0
	for i := 0; i < n; i++ {
		// Cycle through sources
		source := sources[sourceIdx]
		if len(source) == 0 {
			// Skip empty sources
			sourceIdx = (sourceIdx + 1) % len(sources)
			source = sources[sourceIdx]
		}

		// Determine sign
		sign := 1.0
		if *seed&0x8000 != 0 {
			sign = -1.0
		}
		*seed = *seed*1664525 + 1013904223

		// Copy with wrapping
		if len(source) > 0 {
			result[i] = sign * source[i%len(source)]
		}

		sourceIdx = (sourceIdx + 1) % len(sources)
	}

	return NormalizeVector(result)
}

// ApplyAntiCollapse applies anti-collapse noise injection to a band.
// shape: the band vector (folded or decoded)
// energy: the band energy level
// prevEnergy1: energy from previous frame
// prevEnergy2: energy from two frames ago
// seed: RNG state
// gain: anti-collapse gain factor
// Returns: modified shape vector with noise injected.
//
// Anti-collapse prevents artifacts when a band that had energy in previous
// frames suddenly receives no pulses (collapses). A small amount of shaped
// noise is added to mask the sudden silence.
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/bands.c anti_collapse()
func ApplyAntiCollapse(shape []float64, energy, prevEnergy1, prevEnergy2, gain float64, seed *uint32) []float64 {
	if len(shape) == 0 || gain <= 0 {
		return shape
	}

	// Determine minimum energy from recent frames
	minPrev := prevEnergy1
	if prevEnergy2 < minPrev {
		minPrev = prevEnergy2
	}

	// Only apply if there's a significant energy drop
	if energy >= minPrev {
		return shape
	}

	// Generate noise scaled by gain
	result := make([]float64, len(shape))
	copy(result, shape)

	for i := range result {
		// Generate noise sample
		*seed = *seed*1664525 + 1013904223
		noise := float64(int32(*seed)) / float64(1<<31)

		// Mix noise with existing shape
		result[i] += noise * gain
	}

	// Re-normalize after noise injection
	return NormalizeVector(result)
}
