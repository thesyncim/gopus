package silk

import "math"

// Gain encoding matching libopus silk/encode_indices.c and silk/gain_quant.c
// Implements proper delta coding with double step size for large gains.

// encodeSubframeGains quantizes and encodes gains for all subframes.
// First subframe: absolute (MSB + LSB) if first frame, delta-coded if subsequent frame.
// Subsequent subframes: always delta-coded.
// Uses libopus quantization algorithm with hysteresis and double step sizes.
func (e *Encoder) encodeSubframeGains(gains []float32, signalType, numSubframes int) {
	// Convert gains to Q16 format
	gainsQ16 := make([]int32, numSubframes)
	for i := 0; i < numSubframes; i++ {
		// Convert float to Q16
		gainsQ16[i] = int32(gains[i] * 65536.0)
		if gainsQ16[i] < (1 << 16) {
			gainsQ16[i] = 1 << 16 // Minimum gain of 1.0
		}
	}

	// Determine if we're using conditional coding
	conditional := e.haveEncoded

	// Quantize gains using libopus algorithm
	// This updates gainsQ16 in-place to the quantized values
	ind, newPrevInd := silkGainsQuant(gainsQ16, int8(e.previousGainIndex), conditional, numSubframes)

	// Encode gain indices
	if !conditional {
		// First frame: encode absolute gain (MSB + LSB) for first subframe
		// The index is already in [0, N_LEVELS_QGAIN-1] range
		firstIndex := int(e.previousGainIndex) + int(ind[0]) - minDeltaGainQuant
		if firstIndex < 0 {
			firstIndex = 0
		}
		if firstIndex > nLevelsQGain-1 {
			firstIndex = nLevelsQGain - 1
		}
		// Actually, for independent coding, ind[0] is the full index
		// When conditional==false, ind[0] stores the absolute index after quantization
		e.encodeAbsoluteGainIndex(int(ind[0]), signalType)
	} else {
		// Conditional coding: first subframe uses delta_gain_iCDF
		// ind[0] is already shifted by -MIN_DELTA_GAIN_QUANT so it's in [0, 40] range
		e.rangeEncoder.EncodeICDF(int(ind[0]), silk_delta_gain_iCDF, 8)
	}

	// Remaining subframes: always delta-coded
	for i := 1; i < numSubframes; i++ {
		// ind[i] is already shifted by -MIN_DELTA_GAIN_QUANT so it's in [0, 40] range
		e.rangeEncoder.EncodeICDF(int(ind[i]), silk_delta_gain_iCDF, 8)
	}

	// Update state for next frame
	e.previousGainIndex = int32(newPrevInd)
	e.previousLogGain = int32(newPrevInd)
}

// encodeAbsoluteGainIndex encodes the absolute gain index for first subframe.
// Uses libopus tables: silk_gain_iCDF[signalType] for MSB, silk_uniform8_iCDF for LSB
// Matches libopus silk/encode_indices.c lines 77-79
func (e *Encoder) encodeAbsoluteGainIndex(gainIndex, signalType int) {
	// Clamp to valid range
	if gainIndex < 0 {
		gainIndex = 0
	}
	if gainIndex > nLevelsQGain-1 {
		gainIndex = nLevelsQGain - 1
	}

	// Split into MSB (0-7) and LSB (0-7)
	// MSB = gainIndex >> 3 (divide by 8)
	// LSB = gainIndex & 7 (modulo 8)
	msb := gainIndex >> 3
	lsb := gainIndex & 7

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

// encodeFirstGainIndex is kept for backwards compatibility
// Uses libopus tables: silk_gain_iCDF[signalType] for MSB, silk_uniform8_iCDF for LSB
func (e *Encoder) encodeFirstGainIndex(gainIndex, signalType int) {
	e.encodeAbsoluteGainIndex(gainIndex, signalType)
}

// computeSubframeGains computes gains for each subframe from PCM.
// Returns gains in linear domain matching libopus energy computation.
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

		// Compute energy (sum of squares)
		var energy float64
		for i := start; i < end; i++ {
			energy += float64(pcm[i]) * float64(pcm[i])
		}

		// Normalize by number of samples
		n := end - start
		if n > 0 {
			energy /= float64(n)
		}

		// Convert to RMS gain
		if energy > 0 {
			gains[sf] = float32(math.Sqrt(energy))
		} else {
			gains[sf] = 1.0 // Minimum gain
		}

		// Ensure minimum gain
		if gains[sf] < 1.0 {
			gains[sf] = 1.0
		}
	}

	return gains
}

// computeSubframeGainsQ16 computes gains for each subframe from PCM.
// Returns gains in Q16 format matching libopus.
func (e *Encoder) computeSubframeGainsQ16(pcm []int16, numSubframes int) []int32 {
	if len(pcm) == 0 || numSubframes <= 0 {
		gains := make([]int32, numSubframes)
		for i := range gains {
			gains[i] = 1 << 16 // Default gain of 1.0
		}
		return gains
	}

	subframeSamples := len(pcm) / numSubframes
	gains := make([]int32, numSubframes)

	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeSamples
		end := start + subframeSamples
		if end > len(pcm) {
			end = len(pcm)
		}

		gains[sf] = GainQ16FromPCM(pcm[start:end], end-start)
	}

	return gains
}

// computeLogGainIndexQ16 converts Q16 linear gain to log gain index [0, 63].
// Uses the libopus logarithmic quantization.
func computeLogGainIndexQ16(gainQ16 int32) int {
	if gainQ16 <= 0 {
		return 0
	}

	// Use libopus log2lin/lin2log for accurate conversion
	logGain := silkLin2Log(gainQ16)

	// Scale to index: ind = SMULWB(SCALE_Q16, logGain - OFFSET)
	ind := silkSMULWB(int32(gainScaleQ16), logGain-int32(gainOffsetQ7))

	// Clamp to valid range
	if ind < 0 {
		return 0
	}
	if ind > nLevelsQGain-1 {
		return nLevelsQGain - 1
	}

	return int(ind)
}

// computeLogGainIndex converts linear gain to log gain index [0, 63].
// Uses the libopus logarithmic quantization.
func computeLogGainIndex(gain float32) int {
	if gain <= 0 {
		return 0
	}

	// Convert float gain to Q16
	gainQ16 := int32(gain * 65536.0)

	return computeLogGainIndexQ16(gainQ16)
}

// computeFirstFrameGainIndex computes gain index for first frame encoding.
// For libopus, when conditional=false, the absolute index is stored directly.
func (e *Encoder) computeFirstFrameGainIndex(targetLogGain int) int {
	// The target log gain is already in [0, 63] range
	if targetLogGain < 0 {
		return 0
	}
	if targetLogGain > nLevelsQGain-1 {
		return nLevelsQGain - 1
	}
	return targetLogGain
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
