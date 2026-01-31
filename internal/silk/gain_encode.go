package silk

import "math"

// encodeSubframeGains quantizes and encodes gains for all subframes.
// First subframe: absolute (MSB + LSB) if first frame, delta-coded if subsequent frame.
// Subsequent subframes: always delta-coded.
// Per RFC 6716 Section 4.2.7.4.
// Uses libopus tables: silk_gain_iCDF, silk_uniform8_iCDF, silk_delta_gain_iCDF
func (e *Encoder) encodeSubframeGains(gains []float32, signalType, numSubframes int) {
	// Convert gains to log domain and quantize
	logGains := make([]int, numSubframes)
	for i := 0; i < numSubframes; i++ {
		logGains[i] = computeLogGainIndex(gains[i])
	}

	// First subframe encoding depends on whether we're conditioning on previous frame
	firstLogGain := logGains[0]
	var condCoding bool

	if !e.haveEncoded {
		// First frame: encode absolute gain (MSB + LSB)
		condCoding = false
		gainIndex := e.computeFirstFrameGainIndex(firstLogGain)
		e.encodeFirstGainIndex(gainIndex, signalType)
	} else {
		// Subsequent frames: encode first subframe as delta from previous frame
		condCoding = true
		// Delta = logGain - prevLogGain + delta_offset
		// The delta_gain_iCDF has 41 symbols, so delta can be 0-40
		// Per libopus: delta_gain = silk_LIMIT_int( ind, 0, MAX_DELTA_GAIN_QUANT-1 )
		// MAX_DELTA_GAIN_QUANT = 41
		delta := firstLogGain - int(e.previousLogGain) + maxDeltaGainQuant/2
		if delta < 0 {
			delta = 0
		}
		if delta > maxDeltaGainQuant-1 {
			delta = maxDeltaGainQuant - 1
		}
		e.rangeEncoder.EncodeICDF(delta, silk_delta_gain_iCDF, 8)
	}

	// Subsequent subframes: always delta-coded using silk_delta_gain_iCDF
	prevLogGain := firstLogGain
	for i := 1; i < numSubframes; i++ {
		// Delta = logGain - prevLogGain + delta_offset
		delta := logGains[i] - prevLogGain + maxDeltaGainQuant/2
		if delta < 0 {
			delta = 0
		}
		if delta > maxDeltaGainQuant-1 {
			delta = maxDeltaGainQuant - 1
		}

		e.rangeEncoder.EncodeICDF(delta, silk_delta_gain_iCDF, 8)

		// Update prevLogGain: decoded_delta - offset = actual delta
		// But we need to track what the decoder will compute
		prevLogGain = logGains[i]
	}

	// Update state for next frame
	e.previousLogGain = int32(logGains[numSubframes-1])

	// Suppress unused variable warning
	_ = condCoding
}


// computeLogGainIndex converts linear gain to log gain index [0, 63].
// Finds the index in GainDequantTable that best matches the input gain.
//
// The GainDequantTable contains Q16 gain values. Given a float32 gain,
// we convert it to Q16 and find the closest table entry.
func computeLogGainIndex(gain float32) int {
	if gain <= 0 {
		return 0
	}

	// Convert float gain to Q16
	gainQ16 := int32(gain * 65536.0)

	// Clamp to table range
	if gainQ16 < GainDequantTable[0] {
		return 0
	}
	if gainQ16 >= GainDequantTable[63] {
		return 63
	}

	// Binary search for closest index
	// GainDequantTable is sorted in increasing order
	lo, hi := 0, 63
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if GainDequantTable[mid] <= gainQ16 {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	// Check if next index is closer
	if lo < 63 {
		diffLo := absInt32(GainDequantTable[lo] - gainQ16)
		diffHi := absInt32(GainDequantTable[lo+1] - gainQ16)
		if diffHi < diffLo {
			return lo + 1
		}
	}

	return lo
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
// Uses libopus tables: silk_gain_iCDF[signalType] for MSB, silk_uniform8_iCDF for LSB
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

	// Clamp MSB to table size (silk_gain_iCDF tables have 8 symbols)
	if msb > 7 {
		msb = 7
	}

	// Encode MSB using libopus silk_gain_iCDF[signalType]
	// signalType: 0=inactive, 1=unvoiced, 2=voiced
	safeSignalType := signalType
	if safeSignalType < 0 || safeSignalType > 2 {
		safeSignalType = 0
	}
	e.rangeEncoder.EncodeICDF(msb, silk_gain_iCDF[safeSignalType], 8)

	// Encode LSB using libopus silk_uniform8_iCDF
	e.rangeEncoder.EncodeICDF(lsb, silk_uniform8_iCDF, 8)
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

// computeLogGainIndexQ16 converts Q16 gain to log gain index [0, 63].
// This is the Q16 version of computeLogGainIndex.
func computeLogGainIndexQ16(gainQ16 int32) int {
	if gainQ16 <= 0 {
		return 0
	}

	// Clamp to table range
	if gainQ16 < GainDequantTable[0] {
		return 0
	}
	if gainQ16 >= GainDequantTable[63] {
		return 63
	}

	// Binary search for closest index
	// GainDequantTable is sorted in increasing order
	lo, hi := 0, 63
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if GainDequantTable[mid] <= gainQ16 {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	// Check if next index is closer
	if lo < 63 {
		diffLo := absInt32(GainDequantTable[lo] - gainQ16)
		diffHi := absInt32(GainDequantTable[lo+1] - gainQ16)
		if diffHi < diffLo {
			return lo + 1
		}
	}

	return lo
}

// absInt returns the absolute value of an integer.
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
