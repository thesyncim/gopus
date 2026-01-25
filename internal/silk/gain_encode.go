package silk

import "math"

// encodeSubframeGains quantizes and encodes gains for all subframes.
// First subframe is absolute (MSB + LSB), subsequent are delta-coded.
// Per RFC 6716 Section 4.2.7.4.
// Uses existing ICDF tables: ICDFGainMSB*, ICDFGainLSB, ICDFDeltaGain
func (e *Encoder) encodeSubframeGains(gains []float32, signalType, numSubframes int) {
	// Convert gains to log domain and quantize
	logGains := make([]int, numSubframes)
	for i := 0; i < numSubframes; i++ {
		logGains[i] = computeLogGainIndex(gains[i])
	}

	// First subframe: encode absolute gain
	firstLogGain := logGains[0]

	// Apply first-frame gain limiter if this is first frame
	if !e.haveEncoded {
		// Per RFC 6716 Section 4.2.7.4.1:
		// gainIndex = logGain + 2*max(0, logGain - 16) for first frame
		// So we need to reverse: given logGain, find gainIndex
		gainIndex := e.computeFirstFrameGainIndex(firstLogGain)
		e.encodeFirstGainIndex(gainIndex, signalType)
	} else {
		// Delta from previous frame's log gain
		// gainIndex = logGain - prevLogGain + 16
		gainIndex := firstLogGain - int(e.previousLogGain) + 16
		if gainIndex < 0 {
			gainIndex = 0
		}
		if gainIndex > 63 {
			gainIndex = 63
		}
		e.encodeFirstGainIndex(gainIndex, signalType)
	}

	// Subsequent subframes: delta-coded using ICDFDeltaGain
	prevLogGain := firstLogGain
	for i := 1; i < numSubframes; i++ {
		// Delta = logGain - prevLogGain + 4
		delta := logGains[i] - prevLogGain + 4
		if delta < 0 {
			delta = 0
		}
		if delta > 15 {
			delta = 15
		}

		e.rangeEncoder.EncodeICDF16(delta, ICDFDeltaGain, 8)
		prevLogGain += delta - 4 // Update for next iteration
	}

	// Update state for next frame
	e.previousLogGain = int32(prevLogGain)
}

// computeLogGainIndex converts linear gain to log gain index [0, 63].
// Uses logarithmic quantization matching the decoder's dequantization formula.
//
// The decoder's gain dequantization (per RFC 6716 Section 4.2.7.4):
//
//	gain = 2^(logGainIndex/8 - 1)
//
// Inverting this: logGainIndex = 8 * (log2(gain) + 1)
//
// This provides much better accuracy than linear search against GainDequantTable,
// especially for high gain values where the table has large gaps.
func computeLogGainIndex(gain float32) int {
	if gain <= 0 {
		return 0
	}

	// Compute log-domain gain index matching decoder's formula
	// Decoder: gain_Q16 = 2^(idx/8 + 6.34)
	// Linear gain = gain_Q16 / 65536 = 2^(idx/8 + 6.34 - 16) = 2^(idx/8 - 9.66)
	// Inverse: idx = 8 * (log2(gain) + 9.66)
	logGain := math.Log2(float64(gain))
	idx := int(math.Round((logGain + 9.66) * 8.0))

	// Clamp to valid range [0, 63]
	if idx < 0 {
		idx = 0
	}
	if idx > 63 {
		idx = 63
	}

	return idx
}

// computeFirstFrameGainIndex computes gain index for first frame encoding.
// Reverses the limiter: logGain = max(0, gainIndex - 2*max(0, gainIndex - 16))
func (e *Encoder) computeFirstFrameGainIndex(targetLogGain int) int {
	// Search for gainIndex that produces targetLogGain after limiter
	for gainIndex := 0; gainIndex <= 63; gainIndex++ {
		var logGain int
		if gainIndex > 16 {
			logGain = gainIndex - 2*(gainIndex-16)
		} else {
			logGain = gainIndex
		}
		if logGain < 0 {
			logGain = 0
		}

		if logGain == targetLogGain {
			return gainIndex
		}
	}
	return targetLogGain // Fallback
}

// encodeFirstGainIndex encodes the absolute gain index for first subframe.
// Uses existing ICDF tables: ICDFGainMSBInactive, ICDFGainMSBUnvoiced, ICDFGainMSBVoiced, ICDFGainLSB
func (e *Encoder) encodeFirstGainIndex(gainIndex, signalType int) {
	// Clamp to valid range
	if gainIndex < 0 {
		gainIndex = 0
	}
	if gainIndex > 63 {
		gainIndex = 63
	}

	// Split into MSB (0-7) and LSB (0-7)
	msb := gainIndex / 8
	lsb := gainIndex % 8

	// Select MSB table based on signal type
	switch signalType {
	case 0: // Inactive
		e.rangeEncoder.EncodeICDF16(msb, ICDFGainMSBInactive, 8)
	case 1: // Unvoiced
		e.rangeEncoder.EncodeICDF16(msb, ICDFGainMSBUnvoiced, 8)
	case 2: // Voiced
		e.rangeEncoder.EncodeICDF16(msb, ICDFGainMSBVoiced, 8)
	}

	// Encode LSB
	e.rangeEncoder.EncodeICDF16(lsb, ICDFGainLSB, 8)
}

// computeSubframeGains computes gains for each subframe from PCM.
// Returns gains in linear domain.
func (e *Encoder) computeSubframeGains(pcm []float32, numSubframes int) []float32 {
	if len(pcm) == 0 || numSubframes <= 0 {
		return make([]float32, numSubframes)
	}

	subframeSamples := len(pcm) / numSubframes
	gains := make([]float32, numSubframes)

	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeSamples
		end := start + subframeSamples
		if end > len(pcm) {
			end = len(pcm)
		}

		// Compute RMS energy
		var energy float64
		for i := start; i < end; i++ {
			energy += float64(pcm[i]) * float64(pcm[i])
		}
		if end > start {
			energy /= float64(end - start)
		}

		// Convert to gain (scale factor for unit energy)
		if energy > 0 {
			gains[sf] = float32(math.Sqrt(energy))
		} else {
			gains[sf] = 1.0 // Minimum gain
		}
	}

	return gains
}

func absInt32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
