package silk

// ltpSynthesis applies long-term prediction to excitation for voiced frames.
// Per RFC 6716 Section 4.2.7.9.1.
//
// LTP predicts current samples from pitch-delayed past output using a 5-tap filter.
// The prediction is added to the excitation to incorporate pitch periodicity.
//
// The LTP filter equation:
//
//	pred[n] = sum(ltpCoeffs[k] * history[n - pitchLag + k - 2]) for k=0..4
//
// Parameters:
//   - excitation: input/output excitation signal (modified in place)
//   - pitchLag: pitch period in samples
//   - ltpCoeffs: Q7 filter coefficients (5 taps)
//   - ltpScale: scaling factor (0, 1, or 2) for gain adjustment
func (d *Decoder) ltpSynthesis(excitation []int32, pitchLag int, ltpCoeffs []int8, ltpScale int) {
	if pitchLag < 2 {
		// Invalid pitch lag, skip LTP
		return
	}

	// LTP scale factors (Q14 format)
	// Per RFC 6716 Section 4.2.7.9.1: 1.0, 0.9375, 0.875
	ltpScaleFactors := []int32{16384, 15360, 14336}
	scale := ltpScaleFactors[ltpScale]

	historyLen := len(d.outputHistory)

	for i := range excitation {
		var pred int64

		// 5-tap filter: taps at [-2, -1, 0, +1, +2] relative to pitchLag
		for k := 0; k < 5; k++ {
			// History index: current position in history - pitchLag + k - 2
			// We read from circular buffer relative to current write position
			histIdx := d.historyIndex - pitchLag + k - 2 + i
			for histIdx < 0 {
				histIdx += historyLen
			}
			histIdx = histIdx % historyLen

			// Multiply: Q7 coeff * history value
			// History is in float32 normalized [-1, 1], convert to Q7 equivalent
			histVal := int64(d.outputHistory[histIdx] * 128.0) // Scale to Q7
			pred += int64(ltpCoeffs[k]) * histVal
		}

		// pred is now in Q14 (Q7 * Q7)
		// Apply LTP scale factor (Q14) -> Q28, then shift down
		pred = (pred * int64(scale)) >> 21 // Q28 -> Q7, then >> 7 for final

		// Add prediction to excitation
		excitation[i] += int32(pred)
	}
}

// updateHistory adds samples to the circular output history buffer.
// This must be called after synthesizing each subframe to maintain
// history for LTP lookback in subsequent subframes/frames.
func (d *Decoder) updateHistory(samples []float32) {
	historyLen := len(d.outputHistory)
	for _, s := range samples {
		d.outputHistory[d.historyIndex] = s
		d.historyIndex = (d.historyIndex + 1) % historyLen
	}
}

// getHistorySample retrieves a sample from the output history buffer.
// Offset is how many samples back from the current position (positive = past).
func (d *Decoder) getHistorySample(offset int) float32 {
	historyLen := len(d.outputHistory)
	idx := d.historyIndex - offset
	for idx < 0 {
		idx += historyLen
	}
	idx = idx % historyLen
	return d.outputHistory[idx]
}
