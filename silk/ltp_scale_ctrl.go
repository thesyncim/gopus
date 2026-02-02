package silk

import "math"

// computeLTPPredGainQ7 estimates the LTP prediction gain in dB (Q7).
// This approximates libopus pred_gain_dB_Q7 by comparing input energy
// to residual energy after applying the quantized LTP filter.
func computeLTPPredGainQ7(pcm []float32, pitchLags []int, ltpCoeffs LTPCoeffsArray, numSubframes, subframeSamples int) int32 {
	if len(pcm) == 0 || numSubframes <= 0 || len(pitchLags) == 0 {
		return 0
	}

	maxSubframes := numSubframes
	if maxSubframes > len(pitchLags) {
		maxSubframes = len(pitchLags)
	}
	if maxSubframes > len(ltpCoeffs) {
		maxSubframes = len(ltpCoeffs)
	}

	var energy float64
	var residual float64
	for sf := 0; sf < maxSubframes; sf++ {
		lag := pitchLags[sf]
		if lag <= 0 {
			continue
		}
		start := sf * subframeSamples
		end := start + subframeSamples
		if end > len(pcm) {
			end = len(pcm)
		}
		for i := start; i < end; i++ {
			x := float64(pcm[i])
			energy += x * x

			pred := 0.0
			for tap := 0; tap < ltpOrderConst; tap++ {
				idx := i - lag + tap - ltpOrderConst/2
				if idx < 0 || idx >= len(pcm) {
					continue
				}
				pred += float64(ltpCoeffs[sf][tap]) * (1.0 / 128.0) * float64(pcm[idx])
			}
			res := x - pred
			residual += res * res
		}
	}

	if energy <= 0 || residual <= 0 {
		return 0
	}

	gainDb := 10.0 * math.Log10(energy/residual)
	if gainDb < 0 {
		gainDb = 0
	}

	return int32(gainDb*128.0 + 0.5)
}

// computeLTPScaleIndex selects the LTP scale index using libopus logic.
// For zero packet loss this defaults to index 0 (no scaling).
func (e *Encoder) computeLTPScaleIndex(ltpPredGainQ7 int32, condCoding int) int {
	if condCoding != codeIndependently || ltpPredGainQ7 <= 0 {
		return 0
	}

	roundLoss := e.packetLossPercent * e.nFramesPerPacket
	if roundLoss < 0 {
		roundLoss = 0
	}
	if e.lbrrEnabled {
		roundLoss = 2 + (roundLoss*roundLoss)/100
	}

	threshold1 := silkLog2Lin(int32(128*7+2900) - int32(e.snrDBQ7))
	threshold2 := silkLog2Lin(int32(128*7+3900) - int32(e.snrDBQ7))

	scaledGain := silkSMULBB(ltpPredGainQ7, int32(roundLoss))
	idx := 0
	if scaledGain > threshold1 {
		idx++
	}
	if scaledGain > threshold2 {
		idx++
	}
	if idx < 0 {
		return 0
	}
	if idx > 2 {
		return 2
	}
	return idx
}
