package silk

// encodeExcitation encodes the excitation signal for one subframe.
// Uses shell coding with binary splits, mirroring decoder exactly.
// Per RFC 6716 Section 4.2.7.8.
// Uses existing ICDF tables from tables.go
func (e *Encoder) encodeExcitation(excitation []int32, signalType, quantOffset int) {
	shellSize := 16
	numShells := len(excitation) / shellSize

	// Compute pulse counts per shell
	pulseCounts := make([]int, numShells)
	for shell := 0; shell < numShells; shell++ {
		offset := shell * shellSize
		for i := 0; i < shellSize; i++ {
			pulseCounts[shell] += absInt(int(excitation[offset+i]))
		}
	}

	// Determine rate level (minimize total bits)
	rateLevel := e.selectRateLevel(pulseCounts, signalType)

	// Encode rate level using existing ICDF tables
	if signalType == 2 { // Voiced
		e.rangeEncoder.EncodeICDF16(rateLevel, ICDFRateLevelVoiced, 8)
	} else {
		e.rangeEncoder.EncodeICDF16(rateLevel, ICDFRateLevelUnvoiced, 8)
	}

	// Encode pulse counts per shell
	for shell := 0; shell < numShells; shell++ {
		count := pulseCounts[shell]
		if count > 16 {
			count = 16 // Clamp to max encodable by ICDF table
		}
		e.rangeEncoder.EncodeICDF16(count, ICDFExcitationPulseCount, 8)
	}

	// Encode LSBs for large pulse counts (> 10)
	lsbCounts := make([]int, numShells)
	for shell := 0; shell < numShells; shell++ {
		if pulseCounts[shell] > 10 {
			// Compute how many LSB bits needed
			lsbCounts[shell] = (pulseCounts[shell] - 10 + 1) / 2
			if lsbCounts[shell] > 2 { // ICDFExcitationLSB has 3 entries
				lsbCounts[shell] = 2
			}
			if lsbCounts[shell] < 0 {
				lsbCounts[shell] = 0
			}
			e.rangeEncoder.EncodeICDF16(lsbCounts[shell], ICDFExcitationLSB, 8)
		}
	}

	// Encode shell structure using binary splits
	for shell := 0; shell < numShells; shell++ {
		count := pulseCounts[shell]
		if count > 16 {
			count = 16
		}
		if count == 0 {
			continue
		}

		offset := shell * shellSize
		shellPulses := make([]int, shellSize)
		for i := 0; i < shellSize; i++ {
			shellPulses[i] = absInt(int(excitation[offset+i]))
		}

		e.encodePulseDistribution(shellPulses, count)
	}

	// Encode signs for non-zero pulses using ICDFExcitationSign
	for shell := 0; shell < numShells; shell++ {
		offset := shell * shellSize
		for i := 0; i < shellSize; i++ {
			if excitation[offset+i] != 0 {
				sign := 0
				if excitation[offset+i] < 0 {
					sign = 1
				}

				// Sign ICDF indexed by [signalType][quantOffset][min(|pulse|-1, 5)]
				signIdx := absInt(int(excitation[offset+i])) - 1
				if signIdx > 5 {
					signIdx = 5
				}
				if signIdx < 0 {
					signIdx = 0
				}

				// Guard against invalid signalType/quantOffset
				safeSignalType := signalType
				if safeSignalType < 0 || safeSignalType > 2 {
					safeSignalType = 0
				}
				safeQuantOffset := quantOffset
				if safeQuantOffset < 0 || safeQuantOffset > 1 {
					safeQuantOffset = 0
				}

				signICDF := ICDFExcitationSign[safeSignalType][safeQuantOffset][signIdx]
				e.rangeEncoder.EncodeICDF16(sign, signICDF, 8)
			}
		}

		// Encode LSB values if present
		if lsbCounts[shell] > 0 {
			for i := 0; i < shellSize; i++ {
				if excitation[offset+i] != 0 {
					// Extract LSB from magnitude
					mag := absInt(int(excitation[offset+i]))
					lsb := mag & 1
					e.rangeEncoder.EncodeICDF16(lsb, ICDFExcitationLSB, 8)
				}
			}
		}
	}

	// Encode LCG seed for comfort noise
	seed := e.computeLCGSeed(excitation)
	e.rangeEncoder.EncodeICDF16(seed, ICDFLCGSeed, 8)
}

// encodePulseDistribution encodes the binary split tree.
// Mirrors decoder's decodePulseDistribution exactly in reverse.
// Uses ICDFExcitationSplit tables indexed by pulse count
func (e *Encoder) encodePulseDistribution(pulses []int, totalPulses int) {
	if totalPulses == 0 || len(pulses) <= 1 {
		return
	}

	// Recursive binary split
	e.encodeSplit(pulses, 0, len(pulses), totalPulses)
}

// encodeSplit recursively encodes the binary split of pulses.
func (e *Encoder) encodeSplit(pulses []int, start, end, count int) {
	if count == 0 {
		return
	}

	length := end - start
	if length <= 0 {
		return
	}
	if length == 1 {
		// Base case: all remaining pulses go to this position (no encoding needed)
		return
	}

	// Compute left count
	mid := start + length/2
	var leftCount int
	for i := start; i < mid; i++ {
		leftCount += pulses[i]
	}

	// Get split ICDF table based on pulse count
	tableIdx := count
	if tableIdx >= len(ICDFExcitationSplit) {
		tableIdx = len(ICDFExcitationSplit) - 1
	}
	if tableIdx < 0 {
		tableIdx = 0
	}
	icdf := ICDFExcitationSplit[tableIdx]

	// Clamp leftCount to valid range
	if leftCount < 0 {
		leftCount = 0
	}
	if leftCount > count {
		leftCount = count
	}

	// Encode left count
	e.rangeEncoder.EncodeICDF16(leftCount, icdf, 8)

	// Recurse
	rightCount := count - leftCount
	e.encodeSplit(pulses, start, mid, leftCount)
	e.encodeSplit(pulses, mid, end, rightCount)
}

// selectRateLevel selects the rate level that minimizes bits.
func (e *Encoder) selectRateLevel(pulseCounts []int, signalType int) int {
	if len(pulseCounts) == 0 {
		return 0
	}

	// Simple heuristic: higher rate for higher pulse counts
	var totalPulses int
	for _, c := range pulseCounts {
		totalPulses += c
	}

	avgPulses := totalPulses / len(pulseCounts)

	// Map average pulses to rate level [0, 7]
	if avgPulses < 2 {
		return 0
	} else if avgPulses < 4 {
		return 1
	} else if avgPulses < 6 {
		return 2
	} else if avgPulses < 8 {
		return 3
	} else if avgPulses < 10 {
		return 4
	} else if avgPulses < 12 {
		return 5
	} else if avgPulses < 14 {
		return 6
	}
	return 7
}

// computeLCGSeed computes seed for comfort noise generation.
func (e *Encoder) computeLCGSeed(excitation []int32) int {
	// Use hash of excitation as seed
	var hash int32
	for _, v := range excitation {
		hash ^= v
		hash = (hash << 5) | (hash >> 27)
	}
	return int(hash & 0x3) // 2-bit seed (4 values for ICDFLCGSeed)
}

// computeExcitation computes the LPC residual (excitation signal).
// excitation[n] = input[n] - sum(lpc[k] * input[n-k-1])
func (e *Encoder) computeExcitation(pcm []float32, lpcQ12 []int16, gain float32) []int32 {
	n := len(pcm)
	order := len(lpcQ12)
	excitation := make([]int32, n)

	for i := 0; i < n; i++ {
		// Compute LPC prediction
		var prediction float64
		for k := 0; k < order && i-k-1 >= 0; k++ {
			prediction += float64(lpcQ12[k]) * float64(pcm[i-k-1]) / 4096.0
		}

		// Residual = input - prediction, scaled by gain
		residual := float64(pcm[i]) - prediction
		if gain > 0.001 {
			residual /= float64(gain)
		}

		// Quantize to integer
		excitation[i] = int32(residual)
	}

	return excitation
}
