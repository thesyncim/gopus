package silk

// decodeExcitation decodes the excitation signal for one subframe.
// Per RFC 6716 Section 4.2.7.8, excitation uses shell coding with binary splits.
//
// Shell coding distributes pulse counts across 16-sample blocks using a
// recursive binary split tree. The algorithm:
// 1. Decode rate level (selects ICDF variant based on voice activity)
// 2. Decode pulse count per shell (16-sample block)
// 3. Decode LSBs for large pulse counts (extra precision)
// 4. Decode binary split tree to distribute pulses within each shell
// 5. Decode signs for non-zero pulses
//
// Returns excitation pulses (not yet gain-scaled).
func (d *Decoder) decodeExcitation(subframeSamples int, signalType, quantOffset int) []int32 {
	excitation := make([]int32, subframeSamples)

	// Number of shells = subframeSamples / 16
	shellSize := 16
	numShells := subframeSamples / shellSize

	// Decode rate level (selects ICDF variant based on signal type)
	// Per RFC 6716 Section 4.2.7.8.1
	var rateLevel int
	if signalType == 2 { // Voiced
		rateLevel = d.rangeDecoder.DecodeICDF16(ICDFRateLevelVoiced, 8)
	} else {
		rateLevel = d.rangeDecoder.DecodeICDF16(ICDFRateLevelUnvoiced, 8)
	}

	// Decode pulse counts per shell
	// Each shell (16-sample block) gets its own pulse count
	pulseCounts := make([]int, numShells)
	for shell := 0; shell < numShells; shell++ {
		pulseCounts[shell] = d.rangeDecoder.DecodeICDF16(ICDFExcitationPulseCount, 8)
	}

	// Decode LSBs (extra precision for large pulse counts > 10)
	// Per RFC 6716 Section 4.2.7.8.3
	lsbCounts := make([]int, numShells)
	for shell := 0; shell < numShells; shell++ {
		if pulseCounts[shell] > 10 {
			lsbCounts[shell] = d.rangeDecoder.DecodeICDF16(ICDFExcitationLSB, 8)
		}
	}

	// Decode shell structure using binary splits
	for shell := 0; shell < numShells; shell++ {
		pulseCount := pulseCounts[shell]
		if pulseCount == 0 {
			continue
		}

		// Shell offset in excitation array
		offset := shell * shellSize

		// Decode binary split tree to distribute pulses across 16 samples
		shellPulses := make([]int, shellSize)
		d.decodePulseDistribution(shellPulses, pulseCount)

		// Decode signs for non-zero pulses
		// Per RFC 6716 Section 4.2.7.8.4
		for i := 0; i < shellSize; i++ {
			if shellPulses[i] > 0 {
				// Sign ICDF is indexed by [signalType][quantOffset][min(pulseCount-1, 5)]
				signIdx := shellPulses[i] - 1
				if signIdx > 5 {
					signIdx = 5
				}
				if signIdx < 0 {
					signIdx = 0
				}
				// Guard against invalid signalType/quantOffset (corrupted bitstream)
				safeSignalType := signalType
				if safeSignalType < 0 || safeSignalType > 2 {
					safeSignalType = 0 // Default to inactive
				}
				safeQuantOffset := quantOffset
				if safeQuantOffset < 0 || safeQuantOffset > 1 {
					safeQuantOffset = 0
				}
				signICDF := ICDFExcitationSign[safeSignalType][safeQuantOffset][signIdx]
				sign := d.rangeDecoder.DecodeICDF16(signICDF, 8)
				if sign == 1 {
					shellPulses[i] = -shellPulses[i]
				}
			}

			// Apply LSB if present (doubles magnitude and adds LSB value)
			// Per RFC 6716 Section 4.2.7.8.3
			if lsbCounts[shell] > 0 && shellPulses[i] != 0 {
				lsb := d.rangeDecoder.DecodeICDF16(ICDFExcitationLSB, 8)
				if shellPulses[i] > 0 {
					shellPulses[i] = shellPulses[i]*2 + lsb
				} else {
					shellPulses[i] = shellPulses[i]*2 - lsb
				}
			}

			// Store in excitation array
			excitation[offset+i] = int32(shellPulses[i])
		}
	}

	// Add shaped noise for comfort (LCG-based)
	// Per RFC 6716 Section 4.2.7.8.5
	seed := d.rangeDecoder.DecodeICDF16(ICDFLCGSeed, 8)
	d.addShapedNoise(excitation, int32(seed), rateLevel, signalType)

	return excitation
}

// decodePulseDistribution distributes pulseCount pulses across shellSize samples
// using recursive binary splits per RFC 6716 Section 4.2.7.8.2.
//
// The shell coding algorithm recursively partitions the 16-sample block:
// - First split: 8 vs 8
// - Second split: 4 vs 4 (each half)
// - Third split: 2 vs 2 (each quarter)
// - Fourth split: 1 vs 1 (each pair)
func (d *Decoder) decodePulseDistribution(pulses []int, totalPulses int) {
	if totalPulses == 0 || len(pulses) == 0 {
		return
	}

	// Recursive binary split
	d.decodeSplit(pulses, 0, len(pulses), totalPulses)
}

// decodeSplit recursively splits pulses between left and right halves.
// Uses ICDF tables indexed by total pulse count for probability distribution.
func (d *Decoder) decodeSplit(pulses []int, start, end, count int) {
	if count == 0 {
		return
	}

	// Guard against invalid count (could happen with corrupted bitstream)
	if count < 0 {
		return
	}

	length := end - start
	if length <= 0 {
		return
	}
	if length == 1 {
		// Base case: all remaining pulses go to this position
		pulses[start] = count
		return
	}

	// Decode how many pulses go to left half
	mid := start + length/2

	// Get split ICDF table based on pulse count
	// Tables are indexed 0-16 for counts 0-16+
	tableIdx := count
	if tableIdx >= len(ICDFExcitationSplit) {
		tableIdx = len(ICDFExcitationSplit) - 1
	}
	icdf := ICDFExcitationSplit[tableIdx]

	leftCount := d.rangeDecoder.DecodeICDF16(icdf, 8)

	// Clamp leftCount to valid range [0, count]
	// This guards against corrupted bitstream causing invalid splits
	if leftCount < 0 {
		leftCount = 0
	}
	if leftCount > count {
		leftCount = count
	}
	rightCount := count - leftCount

	// Recurse on both halves
	d.decodeSplit(pulses, start, mid, leftCount)
	d.decodeSplit(pulses, mid, end, rightCount)
}

// addShapedNoise adds comfort noise to the excitation signal.
// Per RFC 6716 Section 4.2.7.8.5.
//
// The noise is shaped using an LCG (Linear Congruential Generator) seeded
// from the bitstream. This ensures deterministic noise across decoder implementations.
func (d *Decoder) addShapedNoise(excitation []int32, seed int32, rateLevel, signalType int) {
	// LCG parameters (same as libopus)
	const (
		lcgMul = 196314165
		lcgAdd = 907633515
	)

	state := seed

	// Noise amplitude depends on rate level
	// Higher rate levels have more pulses, so less noise needed
	// Lower rate levels need more noise fill
	noiseAmp := int32(rateLevel + 1)

	// Voiced frames get less noise to preserve pitch periodicity
	if signalType == 2 {
		noiseAmp = noiseAmp >> 1
	}

	// Add noise to positions with zero excitation
	for i := range excitation {
		if excitation[i] == 0 {
			// Advance LCG
			state = state*lcgMul + lcgAdd

			// Add small noise contribution (scaled by 1/256)
			noise := (state >> 24) - 128 // Range [-128, 127]
			excitation[i] = (int32(noise) * noiseAmp) >> 4
		}
	}
}

// scaleExcitation applies gain to excitation signal for one subframe.
// Per RFC 6716 Section 4.2.7.9.
//
// The gain is in Q16 format. The excitation is scaled and the result
// is returned in the same array (modified in place).
func scaleExcitation(excitation []int32, gain int32) {
	for i := range excitation {
		// Multiply and shift down from Q16
		excitation[i] = (excitation[i] * gain) >> 16
	}
}
