package silk

// lpcSynthesis applies LPC synthesis filter to excitation to produce output.
// Per RFC 6716 Section 4.2.7.9.2.
//
// LPC is an all-pole filter that shapes the excitation spectrum to match
// the speech spectral envelope:
//
//	out[n] = exc[n] + sum(a[k] * out[n-k-1]) for k=0..order-1
//
// Parameters:
//   - excitation: input excitation signal (already gain-scaled)
//   - lpcCoeffs: Q12 LPC coefficients
//   - gain: Q16 subframe gain (for additional scaling if needed)
//   - out: output buffer (must be pre-allocated with same length as excitation)
//
// The filter maintains state across subframes via d.prevLPCValues.
func (d *Decoder) lpcSynthesis(excitation []int32, lpcCoeffs []int16, gain int32, out []float32) {
	order := len(lpcCoeffs)
	if order == 0 || len(excitation) == 0 {
		return
	}

	for i, exc := range excitation {
		// Start with excitation value
		// Apply additional gain scaling here if the excitation wasn't pre-scaled
		sample := int64(exc)

		// Add LPC prediction from previous outputs
		// a[k] is Q12, prev samples need scaling
		for k := 0; k < order; k++ {
			// Get previous output from current buffer or state
			var prev float32
			if i-k-1 >= 0 {
				prev = out[i-k-1]
			} else {
				// Use state from previous frame/subframe
				stateIdx := len(d.prevLPCValues) + (i - k - 1)
				if stateIdx >= 0 && stateIdx < len(d.prevLPCValues) {
					prev = d.prevLPCValues[stateIdx]
				}
			}

			// Q12 coeff * output (scaled to fixed-point)
			// prev is in [-1, 1] range, scale to Q12
			prevQ12 := int64(prev * 4096.0)
			sample += (int64(lpcCoeffs[k]) * prevQ12) >> 12
		}

		// Clamp to prevent overflow (16-bit range scaled)
		const maxVal = 32767 * 256
		const minVal = -32768 * 256
		if sample > maxVal {
			sample = maxVal
		}
		if sample < minVal {
			sample = minVal
		}

		// Convert to float32 normalized to [-1, 1]
		out[i] = float32(sample) / float32(maxVal)
	}

	// Update LPC state for next subframe/frame
	// Copy last 'order' samples to state
	d.updateLPCState(out, order)
}

// updateLPCState updates the LPC filter state with new output samples.
// This ensures continuity across subframe/frame boundaries.
func (d *Decoder) updateLPCState(samples []float32, order int) {
	if len(samples) >= order {
		// Copy last 'order' samples directly
		copy(d.prevLPCValues[:order], samples[len(samples)-order:])
	} else {
		// Shift existing state and append new samples
		shift := order - len(samples)
		copy(d.prevLPCValues[:shift], d.prevLPCValues[len(samples):order])
		copy(d.prevLPCValues[shift:], samples)
	}
}

// limitLPCFilterGain applies bandwidth expansion to ensure filter stability.
// Per RFC 6716 Section 4.2.7.5.5.
//
// If the LPC filter has poles too close to the unit circle, the output
// can become unstable (exponential growth). This function iteratively
// applies bandwidth expansion (coefficient decay) until the filter gain
// is within safe bounds.
//
// The function modifies the LPC coefficients in place.
func limitLPCFilterGain(lpc []int16) {
	// Maximum iterations to prevent infinite loop
	const maxIterations = 16

	// Gain threshold in Q24 format
	// This corresponds to a maximum filter gain that keeps poles
	// safely inside the unit circle
	const gainThreshold = 1 << 24

	for iter := 0; iter < maxIterations; iter++ {
		// Compute sum of squared coefficients as a proxy for filter gain
		// This is a simplified stability check; full check would compute
		// reflection coefficients and verify all are < 1
		var sumSq int64
		for _, c := range lpc {
			sumSq += int64(c) * int64(c)
		}

		// Check if gain is acceptable
		if sumSq < gainThreshold {
			return
		}

		// Apply bandwidth expansion: a[k] *= chirp^k
		// Using chirp = 0.99 in Q15 = 32440
		// This pushes poles toward origin, increasing stability margin
		applyBandwidthExpansion(lpc, 32440)
	}
}

// applyBandwidthExpansion scales LPC coefficients to move poles toward origin.
// chirpQ15 is the expansion factor in Q15 format (32768 = 1.0).
//
// Each coefficient is scaled: a[k] = a[k] * chirp^k
// This effectively applies a frequency-dependent decay that prevents
// poles from being too close to the unit circle.
func applyBandwidthExpansion(lpc []int16, chirpQ15 int32) {
	// Start with chirp^1
	factor := chirpQ15

	for k := range lpc {
		// Scale coefficient: a[k] * factor / 32768
		lpc[k] = int16((int32(lpc[k]) * factor) >> 15)

		// Update factor for next coefficient: factor = factor * chirp / 32768
		factor = (factor * chirpQ15) >> 15
	}
}

// lpcInterpolate interpolates LPC coefficients between two sets.
// Per RFC 6716 Section 4.2.7.9, LPC coefficients can be interpolated
// between subframes for smoother transitions.
//
// Parameters:
//   - lpc0: LPC coefficients at start
//   - lpc1: LPC coefficients at end
//   - alpha: interpolation factor in Q8 (0=lpc0, 256=lpc1)
//
// Returns interpolated LPC coefficients.
func lpcInterpolate(lpc0, lpc1 []int16, alpha int32) []int16 {
	if len(lpc0) != len(lpc1) {
		return nil
	}

	result := make([]int16, len(lpc0))
	beta := 256 - alpha // Complement

	for i := range lpc0 {
		// Weighted average: (lpc0 * beta + lpc1 * alpha + 128) >> 8
		val := (int32(lpc0[i])*beta + int32(lpc1[i])*alpha + 128) >> 8
		result[i] = int16(val)
	}

	return result
}

// lpcResidual computes the LPC residual (inverse filter) for analysis.
// This is the inverse of lpcSynthesis - it extracts the excitation from
// a signal given the LPC coefficients.
//
// This function is useful for encoder analysis and testing.
//
//	residual[n] = signal[n] - sum(a[k] * signal[n-k-1]) for k=0..order-1
func lpcResidual(signal []float32, lpcCoeffs []int16, residual []int32) {
	order := len(lpcCoeffs)

	for i := range signal {
		// Start with signal sample (scaled to fixed-point)
		sample := int64(signal[i] * 32768.0 * 256.0)

		// Subtract LPC prediction
		for k := 0; k < order; k++ {
			if i-k-1 >= 0 {
				prevQ12 := int64(signal[i-k-1] * 4096.0)
				sample -= (int64(lpcCoeffs[k]) * prevQ12) >> 12
			}
		}

		// Store residual
		residual[i] = int32(sample >> 8)
	}
}
