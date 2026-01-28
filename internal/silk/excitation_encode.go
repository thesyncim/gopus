package silk

import "math"

// SHELL_CODEC_FRAME_LENGTH is the shell block size per libopus
const SHELL_CODEC_FRAME_LENGTH = 16

// SILK_MAX_PULSES is the maximum encodable pulses per shell block
const SILK_MAX_PULSES = 16

// N_RATE_LEVELS is the number of rate level options
const N_RATE_LEVELS = 10

// encodePulses encodes quantization indices of excitation for the entire frame.
// This matches libopus silk_encode_pulses() - encoding ALL pulses at once.
// Per RFC 6716 Section 4.2.7.8.
func (e *Encoder) encodePulses(pulses []int32, signalType, quantOffset int) {
	frameLength := len(pulses)

	// Calculate number of shell blocks (iter)
	iter := frameLength / SHELL_CODEC_FRAME_LENGTH
	if iter*SHELL_CODEC_FRAME_LENGTH < frameLength {
		iter++
	}

	// Pad pulses if needed
	paddedPulses := make([]int8, iter*SHELL_CODEC_FRAME_LENGTH)
	for i := 0; i < frameLength && i < len(paddedPulses); i++ {
		// Clamp to int8 range
		p := pulses[i]
		if p > 127 {
			p = 127
		} else if p < -128 {
			p = -128
		}
		paddedPulses[i] = int8(p)
	}

	// Take absolute value of pulses
	absPulses := make([]int, iter*SHELL_CODEC_FRAME_LENGTH)
	for i := 0; i < len(paddedPulses); i++ {
		absPulses[i] = absInt(int(paddedPulses[i]))
	}

	// Calculate sum pulses per shell block with overflow handling
	sumPulses := make([]int, iter)
	nRshifts := make([]int, iter)

	for i := 0; i < iter; i++ {
		offset := i * SHELL_CODEC_FRAME_LENGTH
		nRshifts[i] = 0

		for {
			// Compute sum for this shell block
			var sum int
			for k := 0; k < SHELL_CODEC_FRAME_LENGTH; k++ {
				sum += absPulses[offset+k]
			}

			if sum > SILK_MAX_PULSES {
				// Need to downscale
				nRshifts[i]++
				for k := 0; k < SHELL_CODEC_FRAME_LENGTH; k++ {
					absPulses[offset+k] = absPulses[offset+k] >> 1
				}
			} else {
				sumPulses[i] = sum
				break
			}
		}
	}

	// Select rate level that minimizes total bits
	rateLevelIndex := e.selectOptimalRateLevel(sumPulses, nRshifts, signalType)

	// Encode rate level
	if signalType == 2 { // Voiced
		e.rangeEncoder.EncodeICDF16(rateLevelIndex, ICDFRateLevelVoiced, 8)
	} else {
		e.rangeEncoder.EncodeICDF16(rateLevelIndex, ICDFRateLevelUnvoiced, 8)
	}

	// Encode sum-weighted pulses per shell block
	for i := 0; i < iter; i++ {
		if nRshifts[i] == 0 {
			e.rangeEncoder.EncodeICDF16(sumPulses[i], ICDFExcitationPulseCount, 8)
		} else {
			// Overflow: encode special marker, then nRshifts-1 markers, then final sum
			e.rangeEncoder.EncodeICDF16(SILK_MAX_PULSES+1, ICDFExcitationPulseCount, 8)
			for k := 0; k < nRshifts[i]-1; k++ {
				e.rangeEncoder.EncodeICDF16(SILK_MAX_PULSES+1, ICDFExcitationPulseCount, 8)
			}
			e.rangeEncoder.EncodeICDF16(sumPulses[i], ICDFExcitationPulseCount, 8)
		}
	}

	// Shell encoding (binary splits)
	for i := 0; i < iter; i++ {
		if sumPulses[i] > 0 {
			offset := i * SHELL_CODEC_FRAME_LENGTH
			e.shellEncoder(absPulses[offset : offset+SHELL_CODEC_FRAME_LENGTH])
		}
	}

	// LSB encoding for overflow cases
	for i := 0; i < iter; i++ {
		if nRshifts[i] > 0 {
			offset := i * SHELL_CODEC_FRAME_LENGTH
			nLS := nRshifts[i] - 1
			for k := 0; k < SHELL_CODEC_FRAME_LENGTH; k++ {
				absQ := absInt(int(paddedPulses[offset+k]))
				for j := nLS; j > 0; j-- {
					bit := (absQ >> j) & 1
					e.rangeEncoder.EncodeICDF16(bit, ICDFExcitationLSB, 8)
				}
				bit := absQ & 1
				e.rangeEncoder.EncodeICDF16(bit, ICDFExcitationLSB, 8)
			}
		}
	}

	// Encode signs
	e.encodeSigns(paddedPulses, frameLength, signalType, quantOffset, sumPulses)
}

// shellEncoder encodes 16 pulses using hierarchical binary splits.
// Matches libopus silk_shell_encoder().
func (e *Encoder) shellEncoder(pulses []int) {
	// Level 4: 16 -> 2x8
	pulses8_0 := pulses[0] + pulses[1] + pulses[2] + pulses[3] + pulses[4] + pulses[5] + pulses[6] + pulses[7]
	pulses8_1 := pulses[8] + pulses[9] + pulses[10] + pulses[11] + pulses[12] + pulses[13] + pulses[14] + pulses[15]
	total := pulses8_0 + pulses8_1

	if total == 0 {
		return
	}

	// Encode split at level 4 (16 -> 8+8)
	e.encodeShellSplit(pulses8_0, total)

	// Level 3: 8 -> 2x4 (first half)
	if pulses8_0 > 0 {
		pulses4_0 := pulses[0] + pulses[1] + pulses[2] + pulses[3]
		e.encodeShellSplit(pulses4_0, pulses8_0)

		// Level 2: 4 -> 2x2 (first quarter)
		if pulses4_0 > 0 {
			pulses2_0 := pulses[0] + pulses[1]
			e.encodeShellSplit(pulses2_0, pulses4_0)

			// Level 1: 2 -> 1+1
			if pulses2_0 > 0 {
				e.encodeShellSplit(pulses[0], pulses2_0)
			}

			pulses2_1 := pulses[2] + pulses[3]
			if pulses2_1 > 0 {
				e.encodeShellSplit(pulses[2], pulses2_1)
			}
		}

		// Level 2: 4 -> 2x2 (second quarter)
		pulses4_1 := pulses[4] + pulses[5] + pulses[6] + pulses[7]
		if pulses4_1 > 0 {
			pulses2_2 := pulses[4] + pulses[5]
			e.encodeShellSplit(pulses2_2, pulses4_1)

			if pulses2_2 > 0 {
				e.encodeShellSplit(pulses[4], pulses2_2)
			}

			pulses2_3 := pulses[6] + pulses[7]
			if pulses2_3 > 0 {
				e.encodeShellSplit(pulses[6], pulses2_3)
			}
		}
	}

	// Level 3: 8 -> 2x4 (second half)
	if pulses8_1 > 0 {
		pulses4_2 := pulses[8] + pulses[9] + pulses[10] + pulses[11]
		e.encodeShellSplit(pulses4_2, pulses8_1)

		if pulses4_2 > 0 {
			pulses2_4 := pulses[8] + pulses[9]
			e.encodeShellSplit(pulses2_4, pulses4_2)

			if pulses2_4 > 0 {
				e.encodeShellSplit(pulses[8], pulses2_4)
			}

			pulses2_5 := pulses[10] + pulses[11]
			if pulses2_5 > 0 {
				e.encodeShellSplit(pulses[10], pulses2_5)
			}
		}

		pulses4_3 := pulses[12] + pulses[13] + pulses[14] + pulses[15]
		if pulses4_3 > 0 {
			pulses2_6 := pulses[12] + pulses[13]
			e.encodeShellSplit(pulses2_6, pulses4_3)

			if pulses2_6 > 0 {
				e.encodeShellSplit(pulses[12], pulses2_6)
			}

			pulses2_7 := pulses[14] + pulses[15]
			if pulses2_7 > 0 {
				e.encodeShellSplit(pulses[14], pulses2_7)
			}
		}
	}
}

// encodeShellSplit encodes a binary split: leftCount out of total.
func (e *Encoder) encodeShellSplit(leftCount, total int) {
	if total <= 0 {
		return
	}
	if total > len(ICDFExcitationSplit) {
		total = len(ICDFExcitationSplit)
	}
	if leftCount < 0 {
		leftCount = 0
	}
	if leftCount > total {
		leftCount = total
	}

	icdf := ICDFExcitationSplit[total-1] // 0-indexed
	e.rangeEncoder.EncodeICDF16(leftCount, icdf, 8)
}

// encodeSigns encodes signs for non-zero pulses.
// Matches libopus silk_encode_signs().
func (e *Encoder) encodeSigns(pulses []int8, frameLength, signalType, quantOffset int, sumPulses []int) {
	iter := len(sumPulses)

	for i := 0; i < iter; i++ {
		if sumPulses[i] > 0 {
			offset := i * SHELL_CODEC_FRAME_LENGTH
			for k := 0; k < SHELL_CODEC_FRAME_LENGTH; k++ {
				idx := offset + k
				if idx >= frameLength {
					break
				}
				if pulses[idx] != 0 {
					sign := 0
					if pulses[idx] < 0 {
						sign = 1
					}

					// Sign ICDF indexed by [signalType][quantOffset][min(|pulse|-1, 5)]
					signIdx := absInt(int(pulses[idx])) - 1
					if signIdx > 5 {
						signIdx = 5
					}
					if signIdx < 0 {
						signIdx = 0
					}

					// Guard against invalid indices
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
		}
	}
}

// selectOptimalRateLevel selects rate level that minimizes bits.
func (e *Encoder) selectOptimalRateLevel(sumPulses, nRshifts []int, signalType int) int {
	// Simple heuristic based on average pulse count
	var totalPulses int
	for _, s := range sumPulses {
		totalPulses += s
	}

	if len(sumPulses) == 0 {
		return 0
	}

	avgPulses := totalPulses / len(sumPulses)

	// Map to rate level [0, N_RATE_LEVELS-1]
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
	} else if avgPulses < 16 {
		return 7
	}
	return 8
}

// computeExcitation computes the LPC residual (excitation signal).
// excitation[n] = input[n] - sum(lpc[k] * input[n-k-1])
//
// Note: The excitation is computed WITHOUT gain scaling. The gain is encoded
// separately and applied during decoding. Dividing by gain here would cause
// the decoder to apply gain twice (once during excitation reconstruction,
// once during synthesis), resulting in incorrect signal levels.
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

		// Residual = input - prediction
		residual := float64(pcm[i]) - prediction

		// Quantize residual to integer (do NOT divide by gain - decoder applies gain)
		excitation[i] = int32(math.Round(residual))
	}

	return excitation
}
