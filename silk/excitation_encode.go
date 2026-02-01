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

	// Pad pulses if needed - use scratch buffer
	shellLen := iter * SHELL_CODEC_FRAME_LENGTH
	paddedPulses := ensureInt8Slice(&e.scratchPaddedPulses, shellLen)
	for i := 0; i < shellLen; i++ {
		paddedPulses[i] = 0 // Clear
	}
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

	// Take absolute value of pulses - use scratch buffer
	absPulses := ensureIntSlice(&e.scratchAbsPulses, shellLen)
	for i := 0; i < len(paddedPulses); i++ {
		absPulses[i] = absInt(int(paddedPulses[i]))
	}

	// Calculate sum pulses per shell block with overflow handling - use scratch buffers
	sumPulses := ensureIntSlice(&e.scratchSumPulses, iter)
	nRshifts := ensureIntSlice(&e.scratchNRshifts, iter)

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

	// Encode rate level using libopus tables
	// signalType>>1 maps: 0,1 -> 0 (unvoiced/inactive), 2,3 -> 1 (voiced)
	e.rangeEncoder.EncodeICDF(rateLevelIndex, silk_rate_levels_iCDF[signalType>>1], 8)

	// Encode sum-weighted pulses per shell block
	// Use rate-level-dependent ICDF table (matching decoder's silk_pulses_per_block_iCDF)
	pulseCountICDF := silk_pulses_per_block_iCDF[rateLevelIndex]
	for i := 0; i < iter; i++ {
		if nRshifts[i] == 0 {
			e.rangeEncoder.EncodeICDF(sumPulses[i], pulseCountICDF, 8)
		} else {
			// Overflow: encode special marker, then nRshifts-1 markers, then final sum
			e.rangeEncoder.EncodeICDF(SILK_MAX_PULSES+1, pulseCountICDF, 8)
			for k := 0; k < nRshifts[i]-1; k++ {
				e.rangeEncoder.EncodeICDF(SILK_MAX_PULSES+1, silk_pulses_per_block_iCDF[N_RATE_LEVELS-1], 8)
			}
			e.rangeEncoder.EncodeICDF(sumPulses[i], silk_pulses_per_block_iCDF[N_RATE_LEVELS-1], 8)
		}
	}

	// Shell encoding (binary splits)
	for i := 0; i < iter; i++ {
		if sumPulses[i] > 0 {
			offset := i * SHELL_CODEC_FRAME_LENGTH
			e.shellEncoder(absPulses[offset : offset+SHELL_CODEC_FRAME_LENGTH])
		}
	}

	// LSB encoding for overflow cases using libopus table
	for i := 0; i < iter; i++ {
		if nRshifts[i] > 0 {
			offset := i * SHELL_CODEC_FRAME_LENGTH
			nLS := nRshifts[i] - 1
			for k := 0; k < SHELL_CODEC_FRAME_LENGTH; k++ {
				absQ := absInt(int(paddedPulses[offset+k]))
				for j := nLS; j > 0; j-- {
					bit := (absQ >> j) & 1
					e.rangeEncoder.EncodeICDF(bit, silk_lsb_iCDF, 8)
				}
				bit := absQ & 1
				e.rangeEncoder.EncodeICDF(bit, silk_lsb_iCDF, 8)
			}
		}
	}

	// Encode signs
	e.encodeSigns(paddedPulses, frameLength, signalType, quantOffset, sumPulses)
}

// shellEncoder encodes 16 pulses using hierarchical binary splits.
// Matches libopus silk_shell_encoder() exactly.
func (e *Encoder) shellEncoder(pulses []int) {
	// Combine pulses hierarchically, matching libopus silk_shell_encoder.c
	pulses1 := make([]int, 8)
	pulses2 := make([]int, 4)
	pulses3 := make([]int, 2)
	pulses4 := make([]int, 1)

	// Combine: 16 -> 8
	for k := 0; k < 8; k++ {
		pulses1[k] = pulses[2*k] + pulses[2*k+1]
	}
	// Combine: 8 -> 4
	for k := 0; k < 4; k++ {
		pulses2[k] = pulses1[2*k] + pulses1[2*k+1]
	}
	// Combine: 4 -> 2
	for k := 0; k < 2; k++ {
		pulses3[k] = pulses2[2*k] + pulses2[2*k+1]
	}
	// Combine: 2 -> 1
	pulses4[0] = pulses3[0] + pulses3[1]

	// Encode splits using libopus shell tables
	e.encodeShellSplitLibopus(pulses3[0], pulses4[0], silk_shell_code_table3)

	e.encodeShellSplitLibopus(pulses2[0], pulses3[0], silk_shell_code_table2)

	e.encodeShellSplitLibopus(pulses1[0], pulses2[0], silk_shell_code_table1)
	e.encodeShellSplitLibopus(pulses[0], pulses1[0], silk_shell_code_table0)
	e.encodeShellSplitLibopus(pulses[2], pulses1[1], silk_shell_code_table0)

	e.encodeShellSplitLibopus(pulses1[2], pulses2[1], silk_shell_code_table1)
	e.encodeShellSplitLibopus(pulses[4], pulses1[2], silk_shell_code_table0)
	e.encodeShellSplitLibopus(pulses[6], pulses1[3], silk_shell_code_table0)

	e.encodeShellSplitLibopus(pulses2[2], pulses3[1], silk_shell_code_table2)

	e.encodeShellSplitLibopus(pulses1[4], pulses2[2], silk_shell_code_table1)
	e.encodeShellSplitLibopus(pulses[8], pulses1[4], silk_shell_code_table0)
	e.encodeShellSplitLibopus(pulses[10], pulses1[5], silk_shell_code_table0)

	e.encodeShellSplitLibopus(pulses1[6], pulses2[3], silk_shell_code_table1)
	e.encodeShellSplitLibopus(pulses[12], pulses1[6], silk_shell_code_table0)
	e.encodeShellSplitLibopus(pulses[14], pulses1[7], silk_shell_code_table0)
}

// encodeShellSplitLibopus encodes a binary split using libopus shell tables.
// pChild1: pulse count in first child subframe
// p: total pulse count in current subframe
// shellTable: the shell code table to use (e.g., silk_shell_code_table0)
func (e *Encoder) encodeShellSplitLibopus(pChild1, p int, shellTable []uint8) {
	if p > 0 {
		// Get offset into table based on total pulse count
		offset := int(silk_shell_code_table_offsets[p])
		e.rangeEncoder.EncodeICDF(pChild1, shellTable[offset:], 8)
	}
}

// encodeSigns encodes signs for non-zero pulses.
// Matches libopus silk_encode_signs() exactly.
func (e *Encoder) encodeSigns(pulses []int8, frameLength, signalType, quantOffset int, sumPulses []int) {
	// Build 2-element ICDF for sign encoding
	icdf := []uint8{0, 0}

	// Compute index into silk_sign_iCDF table
	// Per libopus: i = 7 * (quantOffsetType + (signalType << 1))
	baseIdx := 7 * (quantOffset + (signalType << 1))
	icdfPtr := silk_sign_iCDF[baseIdx:]

	// Process each shell block
	iter := (frameLength + SHELL_CODEC_FRAME_LENGTH/2) >> 4 // log2(16) = 4
	for i := 0; i < iter; i++ {
		p := sumPulses[i]
		if p > 0 {
			// Set icdf[0] based on sumPulses, clamped to [0, 6]
			pIdx := p & 0x1F
			if pIdx > 6 {
				pIdx = 6
			}
			icdf[0] = icdfPtr[pIdx]

			// Encode sign for each non-zero pulse in this block
			offset := i * SHELL_CODEC_FRAME_LENGTH
			for j := 0; j < SHELL_CODEC_FRAME_LENGTH; j++ {
				idx := offset + j
				if idx >= frameLength {
					break
				}
				if pulses[idx] != 0 {
					// silk_enc_map: negative -> 0, positive -> 1
					// Actually per libopus: silk_enc_map(a) = ( silk_RSHIFT( (a), 15 ) + 1 )
					// For int8, if a < 0, RSHIFT by 15 gives -1, so result is 0
					// If a > 0, RSHIFT by 15 gives 0, so result is 1
					sign := 1
					if pulses[idx] < 0 {
						sign = 0
					}
					e.rangeEncoder.EncodeICDF(sign, icdf, 8)
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
