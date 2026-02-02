package silk

// decodeSubframeGains decodes gains for all subframes.
// Per RFC 6716 Section 4.2.7.4.
//
// First subframe gain is absolute (MSB + LSB).
// Subsequent subframe gains are delta-coded from the previous.
// Returns gains in Q16 format.
func (d *Decoder) decodeSubframeGains(signalType, numSubframes int) []int32 {
	gains := make([]int32, numSubframes)

	// Decode first subframe gain (absolute)
	gainIndex := d.decodeFirstGainIndex(signalType)

	// Convert first gain index to log gain
	// Per libopus silk_gains_dequant: for k==0 && conditional==0:
	//   prev_ind = max(ind[k], prev_ind - 16)
	// This limits how much gain can drop from previous frame's gain.
	// For the very first frame, previousLogGain is initialized to 10.
	var logGain int
	if d.haveDecoded {
		// Conditional coding (first subframe delta from previous frame)
		// prev_ind = max(ind[k], prev_ind - 16)
		minGain := int(d.previousLogGain) - 16
		if minGain < 0 {
			minGain = 0
		}
		logGain = gainIndex
		if logGain < minGain {
			logGain = minGain
		}
	} else {
		// First frame ever - use the decoded index directly
		// (previousLogGain is 0, so max(gainIndex, 0-16) = gainIndex)
		logGain = gainIndex
	}
	// Clamp to valid range
	if logGain < 0 {
		logGain = 0
	}
	if logGain > 63 {
		logGain = 63
	}

	// Convert log gain to Q16 gain
	gains[0] = GainDequantTable[logGain]

	// Decode subsequent subframe gains (delta coded)
	prevLogGain := logGain
	for i := 1; i < numSubframes; i++ {
		// Decode delta gain (centered at 4)
		delta := d.rangeDecoder.DecodeICDF16(ICDFDeltaGain, 8)
		// Delta is in range [0, 15], centered at 4
		// Actual delta = decoded - 4, range [-4, 11]
		prevLogGain += delta - 4

		// Clamp to valid range [0, 63]
		if prevLogGain < 0 {
			prevLogGain = 0
		}
		if prevLogGain > 63 {
			prevLogGain = 63
		}

		gains[i] = GainDequantTable[prevLogGain]
	}

	// Update state for next frame
	d.previousLogGain = int32(prevLogGain)

	return gains
}

// decodeFirstGainIndex decodes the absolute gain index for the first subframe.
// Uses signal-type-specific MSB table and common LSB table.
func (d *Decoder) decodeFirstGainIndex(signalType int) int {
	// Select MSB table based on signal type
	var msb int
	switch signalType {
	case 0: // Inactive
		msb = d.rangeDecoder.DecodeICDF16(ICDFGainMSBInactive, 8)
	case 1: // Unvoiced
		msb = d.rangeDecoder.DecodeICDF16(ICDFGainMSBUnvoiced, 8)
	case 2: // Voiced
		msb = d.rangeDecoder.DecodeICDF16(ICDFGainMSBVoiced, 8)
	}

	// Decode LSB (3 bits = 8 values)
	lsb := d.rangeDecoder.DecodeICDF16(ICDFGainLSB, 8)

	// Combine MSB and LSB to form gain index
	// gainIndex = msb * 8 + lsb, range [0, 63]
	return msb*8 + lsb
}
