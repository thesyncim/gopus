package celt

import "math"

// antiCollapse mirrors libopus celt/bands.c anti_collapse() for float builds.
// It injects shaped noise into collapsed bands for transient frames.
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

	for band := start; band < end; band++ {
		N0 := EBands[band+1] - EBands[band]
		if N0 <= 0 {
			continue
		}
		if band >= len(pulses) {
			break
		}

		depth := celtUdiv(1+pulses[band], N0) >> lm
		thresh := 0.5 * math.Exp2(-0.125*float64(depth))
		sqrt1 := 1.0 / math.Sqrt(float64(N0<<lm))
		bandOffset := EBands[band] << lm
		bandLen := N0 << lm

		for c := 0; c < channels; c++ {
			logIdx := c*(end) + band
			if logIdx >= len(logE) {
				continue
			}
			prevIdx := c*MaxBands + band
			if prevIdx >= len(prev1LogE) || prevIdx >= len(prev2LogE) {
				continue
			}

			prev1 := prev1LogE[prevIdx]
			prev2 := prev2LogE[prevIdx]
			ediff := logE[logIdx] - math.Min(prev1, prev2)
			if ediff < 0 {
				ediff = 0
			}

			r := 2.0 * math.Exp2(-ediff)
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

			mask := collapse[band*channels+c]
			renorm := false
			for k := 0; k < M; k++ {
				if (mask & (1 << uint(k))) != 0 {
					continue
				}
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
			if renorm {
				renormalizeVector(coeffs[bandOffset:bandOffset+bandLen], 1.0)
			}
		}
	}
}
