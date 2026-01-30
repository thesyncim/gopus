package silk

import "math"

// Gain quantization constants from libopus silk/gain_quant.c
// These match the exact fixed-point computation used by libopus.
const (
	// gainQuantOffset = (MIN_QGAIN_DB * 128) / 6 + 16 * 128 = (2*128)/6 + 2048 = 42 + 2048 = 2090
	gainQuantOffset = (minQGainDb*128)/6 + 16*128

	// gainQuantScaleQ16 = 65536 * (N_LEVELS_QGAIN - 1) / (((MAX_QGAIN_DB - MIN_QGAIN_DB) * 128) / 6)
	// = 65536 * 63 / ((86 * 128) / 6) = 4128768 / 1834 = 2251
	gainQuantScaleQ16 = (65536 * (nLevelsQGain - 1)) / (((maxQGainDb - minQGainDb) * 128) / 6)
)

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

// computeLogGainIndex converts linear gain (float32) to log gain index [0, 63].
// This is a wrapper that converts float to Q16 and calls computeLogGainIndexQ16.
//
// The input gain is a linear gain value (typically from RMS energy computation).
// It is converted to Q16 format (gain * 65536) for the fixed-point quantization.
func computeLogGainIndex(gain float32) int {
	if gain <= 0 {
		return 0
	}

	// Convert float gain to Q16 format
	// Q16 means the value is scaled by 65536 (2^16)
	gainQ16 := int32(gain * 65536.0)
	if gainQ16 <= 0 {
		gainQ16 = 1 // Minimum positive value
	}

	return computeLogGainIndexQ16(gainQ16)
}

// computeLogGainIndexQ16 converts Q16 gain to log gain index [0, 63].
// This matches the libopus silk_gains_quant() implementation exactly.
//
// The formula from libopus gain_quant.c:
//
//	ind[k] = silk_SMULWB(SCALE_Q16, silk_lin2log(gain_Q16) - OFFSET)
//
// Where:
//   - OFFSET = (MIN_QGAIN_DB * 128) / 6 + 16 * 128 = 2090
//   - SCALE_Q16 = 65536 * (N_LEVELS_QGAIN - 1) / (((MAX_QGAIN_DB - MIN_QGAIN_DB) * 128) / 6) = 2251
//   - silk_lin2log computes approximately 128 * log2(input)
//   - silk_SMULWB(a, b) = (a * int16(b)) >> 16
func computeLogGainIndexQ16(gainQ16 int32) int {
	if gainQ16 <= 0 {
		return 0
	}

	// Convert to log scale using silk_lin2log (returns ~128 * log2(gainQ16))
	logVal := silkLin2Log(gainQ16)

	// Apply offset and scale
	// SMULWB multiplies by the low 16 bits and right-shifts by 16
	idx := silkSMULWB(gainQuantScaleQ16, logVal-gainQuantOffset)

	// Clamp to valid range [0, 63]
	if idx < 0 {
		idx = 0
	}
	if idx > nLevelsQGain-1 {
		idx = nLevelsQGain - 1
	}

	return int(idx)
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

// absInt returns the absolute value of an integer.
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
