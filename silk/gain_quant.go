package silk

// Gain quantization matching libopus silk/gain_quant.c
// Implements logarithmic gain quantization with hysteresis and delta coding.
// This file contains the encoder-side functions (silkGainsQuant).
// The decoder-side functions (silkGainsDequant) are in libopus_gain.go.
// The log conversion functions (silkLin2Log, silkLog2Lin) are in libopus_log.go.

// Constants for gain quantization
// Using the constants from libopus_consts.go and libopus_gain.go:
// nLevelsQGain = 64, maxDeltaGainQuant = 36, minDeltaGainQuant = -4
// minQGainDb = 2, maxQGainDb = 88
// gainOffsetQ7 = (minQGainDb*128)/6 + 16*128 = 2090
// invScaleQ16Val = (1 << 16) * qgainRangeQ7 / (nLevelsQGain - 1)

// Derived constants matching libopus silk/gain_quant.c:
// SCALE_Q16 = (65536 * (N_LEVELS_QGAIN - 1)) / (((MAX_QGAIN_DB - MIN_QGAIN_DB) * 128) / 6)
//           = (65536 * 63) / ((86 * 128) / 6) = 4128768 / 1834 = 2251
const (
	gainScaleQ16 = (65536 * (nLevelsQGain - 1)) / (((maxQGainDb - minQGainDb) * 128) / 6)
)

// silkGainsQuant quantizes gains matching libopus silk/gain_quant.c:silk_gains_quant
// Parameters:
//   - gainQ16: input gains in Q16 format, will be modified to quantized values
//   - prevInd: previous index from last frame
//   - conditional: if true, first gain is delta-coded from prevInd
//   - nbSubfr: number of subframes
//
// Returns:
//   - ind: gain indices for each subframe
//   - newPrevInd: updated previous index for next frame
func silkGainsQuant(gainQ16 []int32, prevInd int8, conditional bool, nbSubfr int) ([]int8, int8) {
	ind := make([]int8, nbSubfr)
	currentPrevInd := int(prevInd)

	for k := 0; k < nbSubfr; k++ {
		// Convert to log scale, scale, floor()
		// ind[k] = SMULWB(SCALE_Q16, silk_lin2log(gain_Q16[k]) - OFFSET)
		logGain := silkLin2Log(gainQ16[k])
		rawInd := silkSMULWB(int32(gainScaleQ16), logGain-int32(gainOffsetQ7))

		// Round towards previous quantized gain (hysteresis)
		if int(rawInd) < currentPrevInd {
			rawInd++
		}
		rawInd = silkLimit32(rawInd, 0, nLevelsQGain-1)
		ind[k] = int8(rawInd)

		// Compute delta indices and limit
		if k == 0 && !conditional {
			// Full index - limit to not go down too fast
			ind[k] = int8(silkLimitInt(int(ind[k]), currentPrevInd+minDeltaGainQuant, nLevelsQGain-1))
			currentPrevInd = int(ind[k])
		} else {
			// Delta index
			ind[k] = int8(int(ind[k]) - currentPrevInd)

			// Double the quantization step size for large gain increases
			// so that the max gain level can be reached
			doubleStepThreshold := 2*maxDeltaGainQuant - nLevelsQGain + currentPrevInd
			if int(ind[k]) > doubleStepThreshold {
				ind[k] = int8(doubleStepThreshold + (int(ind[k])-doubleStepThreshold+1)>>1)
			}

			ind[k] = int8(silkLimitInt(int(ind[k]), minDeltaGainQuant, maxDeltaGainQuant))

			// Accumulate deltas
			if int(ind[k]) > doubleStepThreshold {
				currentPrevInd += 2*int(ind[k]) - doubleStepThreshold
				if currentPrevInd > nLevelsQGain-1 {
					currentPrevInd = nLevelsQGain - 1
				}
			} else {
				currentPrevInd += int(ind[k])
			}

			// Shift to make non-negative (for encoding)
			ind[k] -= int8(minDeltaGainQuant)
		}

		// Scale and convert to linear scale
		// gain_Q16[k] = silk_log2lin(min(SMULWB(INV_SCALE_Q16, prev_ind) + OFFSET, 3967))
		logQ7 := silkSMULWB(int32(invScaleQ16Val), int32(currentPrevInd)) + int32(gainOffsetQ7)
		if logQ7 > 3967 {
			logQ7 = 3967
		}
		gainQ16[k] = silkLog2Lin(logQ7)
	}

	return ind, int8(currentPrevInd)
}

// silkGainsID computes unique identifier of gain indices vector
// Matches libopus silk/gain_quant.c:silk_gains_ID
func silkGainsID(ind []int8, nbSubfr int) int32 {
	var gainsID int32
	for k := 0; k < nbSubfr; k++ {
		gainsID = (gainsID << 8) + int32(ind[k])
	}
	return gainsID
}

// Note: silkLimitInt is defined in libopus_fixed.go
// Note: silkLimit32 is defined in libopus_fixed.go

// GainQ16FromPCM computes Q16 gain from PCM samples using libopus method.
// This computes the subframe energy and converts to gain matching libopus.
func GainQ16FromPCM(pcm []int16, subframeSamples int) int32 {
	if len(pcm) == 0 || subframeSamples <= 0 {
		return 1 << 16 // Default gain of 1.0
	}

	// Compute sum of squares
	var sumSq int64
	n := subframeSamples
	if n > len(pcm) {
		n = len(pcm)
	}

	for i := 0; i < n; i++ {
		val := int64(pcm[i])
		sumSq += val * val
	}

	// Compute RMS and scale to Q16
	// rms = sqrt(sumSq / n)
	// gain_Q16 = rms * 65536
	if sumSq == 0 {
		return 1 << 16
	}

	// Shift to avoid overflow while maintaining precision
	// Use log domain for accuracy
	energyQ0 := sumSq / int64(n)
	if energyQ0 <= 0 {
		return 1 << 16
	}

	// Approximate sqrt: gain = sqrt(energy)
	// In log domain: log(gain) = log(energy) / 2
	// Then convert back to linear
	logEnergy := silkLin2Log(int32(energyQ0))
	logGain := logEnergy >> 1 // Divide by 2 for sqrt

	// Convert to linear Q16
	gainQ16 := silkLog2Lin(logGain)

	// Clamp to minimum
	if gainQ16 < (1 << 16) {
		gainQ16 = 1 << 16
	}

	return gainQ16
}
