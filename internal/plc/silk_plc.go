package plc

import "gopus/internal/silk"

// ConcealSILK generates concealment audio for a lost SILK frame.
//
// SILK PLC strategy (per RFC 6716 Section 4.2.8):
//  1. Reuse LPC coefficients from last frame
//  2. For voiced frames: continue pitch prediction with decaying gain
//  3. For unvoiced frames: generate comfort noise
//  4. Apply fade factor to output
//
// This provides smooth transitions during packet loss by maintaining
// the spectral characteristics of the last successfully decoded frame.
//
// Parameters:
//   - dec: SILK decoder with state from last good frame
//   - frameSize: samples to generate at native SILK rate (8/12/16kHz)
//   - fadeFactor: gain multiplier (0.0 to 1.0)
//
// Returns: concealed samples at native SILK rate
func ConcealSILK(dec *silk.Decoder, frameSize int, fadeFactor float64) []float32 {
	if dec == nil || frameSize <= 0 {
		return make([]float32, frameSize)
	}

	// If fade is effectively zero, return silence
	if fadeFactor < 0.001 {
		return make([]float32, frameSize)
	}

	output := make([]float32, frameSize)

	// Get state from decoder
	prevLPC := dec.PrevLPCValues()
	order := dec.LPCOrder()
	if order == 0 {
		order = 10 // Default NB/MB order
	}

	wasVoiced := dec.IsPreviousFrameVoiced()

	// RNG state for noise generation
	rng := uint32(22222)

	if wasVoiced {
		// Voiced PLC: use pitch repetition with LPC filtering
		// Get pitch information from history
		concealVoicedSILK(dec, output, prevLPC, order, fadeFactor, &rng)
	} else {
		// Unvoiced PLC: generate comfort noise filtered by LPC
		concealUnvoicedSILK(output, prevLPC, order, fadeFactor, &rng)
	}

	return output
}

// concealVoicedSILK generates concealment for voiced (pitched) speech.
// It extrapolates the pitch pattern from previous frames.
func concealVoicedSILK(dec *silk.Decoder, output []float32, prevLPC []float32, order int, fade float64, rng *uint32) {
	// Get history for pitch repetition
	history := dec.OutputHistory()
	histIdx := dec.HistoryIndex()
	histLen := len(history)

	if histLen == 0 {
		// No history available, fall back to noise
		concealUnvoicedSILK(output, prevLPC, order, fade, rng)
		return
	}

	// Estimate pitch lag from history (simple autocorrelation)
	// Use a basic approach: look for periodicity in last ~10ms
	pitchLag := estimatePitchFromHistory(history, histIdx, histLen)
	if pitchLag < 10 {
		pitchLag = 80 // Default to ~5ms at 16kHz if estimation fails
	}

	// Generate voiced excitation by repeating pitch period
	excitation := make([]float32, len(output))
	for i := range excitation {
		// Get sample from pitch-delayed history
		srcIdx := histIdx - pitchLag + (i % pitchLag)
		for srcIdx < 0 {
			srcIdx += histLen
		}
		srcIdx = srcIdx % histLen

		// Copy with decay
		excitation[i] = history[srcIdx] * float32(fade)

		// Add small noise to prevent pure repetition artifacts
		*rng = *rng*1664525 + 1013904223
		noise := (float32(*rng>>16) - 32768.0) / 32768.0 * 0.01
		excitation[i] += noise * float32(fade)
	}

	// Apply simple smoothing to avoid harsh transitions
	for i := range output {
		output[i] = excitation[i]
	}
}

// concealUnvoicedSILK generates concealment for unvoiced (noise-like) speech.
// It produces comfort noise shaped by the previous LPC filter.
func concealUnvoicedSILK(output []float32, prevLPC []float32, order int, fade float64, rng *uint32) {
	// Generate white noise excitation
	excitation := make([]float32, len(output))
	for i := range excitation {
		*rng = *rng*1664525 + 1013904223
		// Generate noise in [-1, 1] range
		noise := (float32(*rng>>16) - 32768.0) / 65536.0
		excitation[i] = noise * float32(fade)
	}

	// Apply simple LPC filter to shape the noise
	// This gives the noise the spectral character of speech
	if order > 0 && len(prevLPC) >= order {
		state := make([]float32, order)
		copy(state, prevLPC[:order])

		for i := range output {
			// IIR filter: y[n] = x[n] + sum(a[k]*y[n-k-1])
			y := excitation[i]
			for k := 0; k < order && k < len(state); k++ {
				if i-k-1 >= 0 {
					y += state[k] * output[i-k-1] * 0.1
				}
			}
			output[i] = y

			// Clamp to prevent instability
			if output[i] > 1.0 {
				output[i] = 1.0
			} else if output[i] < -1.0 {
				output[i] = -1.0
			}
		}
	} else {
		// No LPC available, just use the noise directly
		copy(output, excitation)
	}
}

// estimatePitchFromHistory tries to find the pitch period in recent history.
// Uses simple autocorrelation to detect periodicity.
func estimatePitchFromHistory(history []float32, histIdx, histLen int) int {
	// Search range: 32 to 288 samples (typical pitch range)
	// At 16kHz: 32 samples = 2ms (500Hz), 288 samples = 18ms (55Hz)
	minLag := 32
	maxLag := 288
	if maxLag > histLen/2 {
		maxLag = histLen / 2
	}
	if minLag >= maxLag {
		return 80 // Default
	}

	// Look at last analysisLen samples
	analysisLen := 320 // ~20ms at 16kHz
	if analysisLen > histLen {
		analysisLen = histLen
	}

	var bestLag int
	var bestCorr float32 = -1e10

	// Simple autocorrelation search
	for lag := minLag; lag < maxLag; lag++ {
		var corr float32

		for i := 0; i < analysisLen-lag; i++ {
			idx1 := (histIdx - analysisLen + i + histLen) % histLen
			idx2 := (histIdx - analysisLen + i + lag + histLen) % histLen
			corr += history[idx1] * history[idx2]
		}

		if corr > bestCorr {
			bestCorr = corr
			bestLag = lag
		}
	}

	if bestLag < minLag {
		bestLag = 80 // Default
	}

	return bestLag
}

// ConcealSILKStereo generates concealment for a stereo SILK frame.
// It applies the same PLC algorithm to both channels.
//
// Parameters:
//   - dec: SILK decoder (used for both channels' state)
//   - frameSize: samples per channel at native SILK rate
//   - fadeFactor: gain multiplier (0.0 to 1.0)
//
// Returns: left and right channel concealed samples
func ConcealSILKStereo(dec *silk.Decoder, frameSize int, fadeFactor float64) (left, right []float32) {
	// For stereo, apply mono PLC to both channels
	// A more sophisticated approach would use the stereo prediction weights
	mono := ConcealSILK(dec, frameSize, fadeFactor)

	// Copy mono to both channels (simple approach)
	// In practice, you'd want to maintain separate L/R state
	left = make([]float32, frameSize)
	right = make([]float32, frameSize)
	copy(left, mono)
	copy(right, mono)

	return left, right
}
