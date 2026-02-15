package celt

import "math"

// antiCollapse mirrors libopus celt/bands.c anti_collapse() for float builds.
// It injects shaped noise into collapsed bands for transient frames.
//
// Parameters:
// - coeffsL, coeffsR: normalized coefficients for left and right channels
// - collapse: collapse mask per band*channel (from quantAllBandsDecode)
// - lm: log mode (0=2.5ms, 1=5ms, 2=10ms, 3=20ms)
// - channels: number of channels (1 or 2)
// - start, end: band range to process
// - logE: current frame's log energies (end bands per channel, indexed as c*end+band)
// - prev1LogE, prev2LogE: previous frames' log energies (MaxBands per channel, indexed as c*MaxBands+band)
// - pulses: bit allocation per band
// - seed: RNG seed for noise generation
func antiCollapse(
	coeffsL, coeffsR []float64,
	collapse []byte,
	lm int,
	channels int,
	start, end int,
	logE, prev1LogE, prev2LogE []float64,
	pulses []int,
	seed uint32,
) {
	if channels < 1 || channels > 2 {
		return
	}
	if start < 0 {
		start = 0
	}
	if end > MaxBands {
		end = MaxBands
	}
	if end <= start {
		return
	}
	if len(collapse) < channels*MaxBands {
		return
	}

	M := 1 << lm
	if M <= 0 {
		return
	}

	// Determine the stride for logE indexing.
	// logE may have 'end' bands per channel (not MaxBands).
	// We compute stride from the array length.
	logEStride := end
	if channels > 0 && len(logE) > 0 {
		logEStride = len(logE) / channels
		if logEStride < end {
			logEStride = end
		}
	}

	for band := start; band < end; band++ {
		N0 := EBands[band+1] - EBands[band]
		if N0 <= 0 {
			continue
		}
		if band >= len(pulses) {
			break
		}

		depth := celtUdiv(1+pulses[band], N0) >> lm
		thresh := 0.5 * float64(celtExp2(float32(-0.125)*float32(depth)))
		sqrt1 := 1.0 / math.Sqrt(float64(N0<<lm))
		bandOffset := EBands[band] << lm
		bandLen := N0 << lm

		for c := 0; c < channels; c++ {
			// Index into logE using the actual stride
			logIdx := c*logEStride + band
			if logIdx >= len(logE) {
				continue
			}
			// Index into prev arrays using MaxBands stride (they are always MaxBands per channel)
			prevIdx := c*MaxBands + band
			if prevIdx >= len(prev1LogE) || prevIdx >= len(prev2LogE) {
				continue
			}

			prev1 := prev1LogE[prevIdx]
			prev2 := prev2LogE[prevIdx]
			// For mono decoding in a stereo stream, use the max of both channels
			// to match libopus anti_collapse() behavior.
			// This is triggered when C==1 but prev arrays have 2*MaxBands entries.
			if channels == 1 && len(prev1LogE) >= 2*MaxBands && len(prev2LogE) >= 2*MaxBands {
				alt1 := prev1LogE[MaxBands+band]
				if alt1 > prev1 {
					prev1 = alt1
				}
				alt2 := prev2LogE[MaxBands+band]
				if alt2 > prev2 {
					prev2 = alt2
				}
			}
			ediff := logE[logIdx] - math.Min(prev1, prev2)
			if ediff < 0 {
				ediff = 0
			}

			// r needs to be multiplied by 2 or 2*sqrt(2) depending on LM because
			// short blocks don't have the same energy as long
			r := 2.0 * float64(celtExp2(float32(-ediff)))
			if lm == 3 {
				r *= 1.41421356
			}
			if r > thresh {
				r = thresh
			}
			r *= sqrt1
			if r <= 0 {
				continue
			}

			var coeffs []float64
			if c == 0 {
				coeffs = coeffsL
			} else {
				coeffs = coeffsR
			}
			if bandOffset+bandLen > len(coeffs) {
				continue
			}

			// Collapse mask is indexed as band*C+c per libopus
			mask := collapse[band*channels+c]
			renorm := false
			for k := 0; k < M; k++ {
				// Check if this sub-block was collapsed (no pulses allocated)
				if (mask & (1 << uint(k))) != 0 {
					continue
				}
				// Fill with pseudo-random noise at amplitude r
				for j := 0; j < N0; j++ {
					seed = seed*1664525 + 1013904223
					if (seed & 0x8000) != 0 {
						coeffs[bandOffset+(j<<lm)+k] = r
					} else {
						coeffs[bandOffset+(j<<lm)+k] = -r
					}
				}
				renorm = true
			}
			// Renormalize the band to unit energy after adding noise
			if renorm {
				renormalizeVector(coeffs[bandOffset:bandOffset+bandLen], 1.0)
			}
		}
	}
}
