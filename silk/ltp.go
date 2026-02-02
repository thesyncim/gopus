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

	// LTP scale factors (linear).
	// Per RFC 6716 Section 4.2.7.9.1: 1.0, 0.9375, 0.875.
	ltpScaleFactors := []float64{1.0, 0.9375, 0.875}
	if ltpScale < 0 || ltpScale >= len(ltpScaleFactors) {
		ltpScale = 0
	}
	scale := ltpScaleFactors[ltpScale]

	historyLen := len(d.outputHistory)

	for i := range excitation {
		var pred float64

		// 5-tap filter centered around pitchLag samples ago.
		// Per libopus NSQ.c: b_Q14[0] is applied to position (-lag + 2),
		// b_Q14[1] to (-lag + 1), ..., b_Q14[4] to (-lag - 2).
		// So coefficient k is applied to history at (-lag + 2 - k).
		for k := 0; k < 5; k++ {
			// History index: current position in history - pitchLag + (2 - k)
			// This matches libopus's pred_lag_ptr[0], pred_lag_ptr[-1], ..., pred_lag_ptr[-4]
			histIdx := d.historyIndex - pitchLag + 2 - k + i
			for histIdx < 0 {
				histIdx += historyLen
			}
			histIdx = histIdx % historyLen

			// History is in float32 normalized [-1, 1].
			histVal := float64(d.outputHistory[histIdx])
			coeff := float64(ltpCoeffs[k]) / 128.0
			pred += coeff * histVal
		}

		// Apply LTP scale factor and convert to PCM units.
		pred *= scale

		// Add prediction to excitation
		excitation[i] += int32(pred * 32768.0)
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
