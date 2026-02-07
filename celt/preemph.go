// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the pre-emphasis filter and DC rejection for encoding.

package celt

// DCRejectCutoffHz is the cutoff frequency for the DC rejection high-pass filter.
// libopus uses 3 Hz at the Opus encoder level.
// Reference: libopus src/opus_encoder.c line 2008
const DCRejectCutoffHz = 3

// DelayCompensation is the number of samples of lookahead for CELT.
// libopus uses Fs/250 = 192 samples at 48kHz (4ms).
// This provides a lookahead that allows for better transient handling.
// Reference: libopus src/opus_encoder.c delay_compensation
const DelayCompensation = 192

// CELTSigScale is the internal signal scale used by CELT.
// Input samples in float range [-1.0, 1.0] are scaled up by this factor
// for internal processing, matching libopus CELT_SIG_SCALE.
const CELTSigScale = 32768.0

// noFMA32Mul forces float32 multiplication with an intermediate rounding step.
// This matches libopus float paths that perform mul and add/sub as separate ops.
func noFMA32Mul(a, b float32) float32 {
	return float32(float64(a) * float64(b))
}

// ApplyPreemphasis applies the pre-emphasis filter to PCM input samples.
// Pre-emphasis boosts high frequencies to improve coding efficiency.
//
// The filter equation is:
//
//	y[n] = x[n] - PreemphCoef * x[n-1]
//
// This is the inverse of the decoder's de-emphasis filter:
//
//	y[n] = x[n] + PreemphCoef * y[n-1]
//
// The filter state is maintained in e.preemphState for frame continuity.
//
// Parameters:
//   - pcm: input PCM samples (interleaved if stereo)
//
// Returns: pre-emphasized samples
//
// Reference: RFC 6716 Section 4.3.5, libopus celt/celt_encoder.c
// Uses PreemphCoef = 0.85 (D03-05-03)
func (e *Encoder) ApplyPreemphasis(pcm []float64) []float64 {
	if len(pcm) == 0 {
		return nil
	}

	output := make([]float64, len(pcm))

	if e.channels == 1 {
		// Mono pre-emphasis
		state := e.preemphState[0]
		for i := range pcm {
			output[i] = pcm[i] - PreemphCoef*state
			state = pcm[i]
		}
		e.preemphState[0] = state
	} else {
		// Stereo pre-emphasis (interleaved samples)
		stateL := e.preemphState[0]
		stateR := e.preemphState[1]

		for i := 0; i < len(pcm)-1; i += 2 {
			// Left channel
			output[i] = pcm[i] - PreemphCoef*stateL
			stateL = pcm[i]

			// Right channel
			output[i+1] = pcm[i+1] - PreemphCoef*stateR
			stateR = pcm[i+1]
		}

		e.preemphState[0] = stateL
		e.preemphState[1] = stateR
	}

	return output
}

// ApplyPreemphasisInPlace applies pre-emphasis in-place to the input samples.
// This is more efficient when a copy is not needed.
func (e *Encoder) ApplyPreemphasisInPlace(pcm []float64) {
	if len(pcm) == 0 {
		return
	}

	if e.channels == 1 {
		// Mono pre-emphasis
		state := e.preemphState[0]
		for i := range pcm {
			x := pcm[i]
			pcm[i] = x - PreemphCoef*state
			state = x
		}
		e.preemphState[0] = state
	} else {
		// Stereo pre-emphasis (interleaved samples)
		stateL := e.preemphState[0]
		stateR := e.preemphState[1]

		for i := 0; i < len(pcm)-1; i += 2 {
			// Left channel
			xL := pcm[i]
			pcm[i] = xL - PreemphCoef*stateL
			stateL = xL

			// Right channel
			xR := pcm[i+1]
			pcm[i+1] = xR - PreemphCoef*stateR
			stateR = xR
		}

		e.preemphState[0] = stateL
		e.preemphState[1] = stateR
	}
}

// applyPreemphasisWithScalingCore applies pre-emphasis with signal scaling to the output buffer.
// This is the shared core logic for both allocating and scratch-based versions.
// Input samples are scaled from float range [-1.0, 1.0] to signal scale
// (multiplied by CELTSigScale = 32768), then the pre-emphasis filter is applied.
func (e *Encoder) applyPreemphasisWithScalingCore(pcm, output []float64) {
	coef := float32(PreemphCoef)

	if e.channels == 1 {
		// Mono pre-emphasis with scaling
		state := float32(e.preemphState[0])
		for i := range pcm {
			// Scale input to signal scale and apply pre-emphasis
			// Match libopus float math: cast to float32 before scaling.
			scaled := float32(pcm[i]) * float32(CELTSigScale)
			output[i] = float64(scaled - noFMA32Mul(coef, state))
			state = scaled
		}
		e.preemphState[0] = float64(state)
	} else {
		// Stereo pre-emphasis (interleaved samples) with scaling
		stateL := float32(e.preemphState[0])
		stateR := float32(e.preemphState[1])

		for i := 0; i < len(pcm)-1; i += 2 {
			// Left channel
			scaledL := float32(pcm[i]) * float32(CELTSigScale)
			output[i] = float64(scaledL - noFMA32Mul(coef, stateL))
			stateL = scaledL

			// Right channel
			scaledR := float32(pcm[i+1]) * float32(CELTSigScale)
			output[i+1] = float64(scaledR - noFMA32Mul(coef, stateR))
			stateR = scaledR
		}

		e.preemphState[0] = float64(stateL)
		e.preemphState[1] = float64(stateR)
	}
}

// ApplyPreemphasisWithScaling applies pre-emphasis with signal scaling.
// Input samples are first scaled from float range [-1.0, 1.0] to signal scale
// (multiplied by CELTSigScale = 32768), then the pre-emphasis filter is applied.
//
// This matches libopus celt_preemphasis() behavior where samples are scaled
// and filtered together. The decoder's scaleSamples(1/32768) reverses the scaling.
func (e *Encoder) ApplyPreemphasisWithScaling(pcm []float64) []float64 {
	if len(pcm) == 0 {
		return nil
	}

	output := make([]float64, len(pcm))
	e.applyPreemphasisWithScalingCore(pcm, output)
	return output
}

// applyDCRejectCore applies DC rejection filter to the output buffer.
// This is the shared core logic for both allocating and scratch-based versions.
func (e *Encoder) applyDCRejectCore(pcm, output []float64) {
	// Coefficients: coef = 6.3 * cutoff / Fs
	// For 48kHz and 3Hz cutoff: coef = 6.3 * 3 / 48000 = 0.00039375
	// Use float32 math to match libopus float path.
	coef := float32(6.3 * float64(DCRejectCutoffHz) / float64(e.sampleRate))
	coef2 := float32(1.0) - coef
	verySmall := float32(1e-30) // Matches VERY_SMALL in libopus float build

	if e.channels == 1 {
		m0 := float32(e.hpMem[0])
		for i := range pcm {
			x := float32(pcm[i])
			y := x - m0
			output[i] = float64(y)
			m0 = coef*x + verySmall + coef2*m0
		}
		e.hpMem[0] = float64(m0)
	} else {
		// Stereo: interleaved samples
		m0 := float32(e.hpMem[0])
		m1 := float32(e.hpMem[1])
		for i := 0; i < len(pcm)-1; i += 2 {
			x0 := float32(pcm[i])
			x1 := float32(pcm[i+1])
			output[i] = float64(x0 - m0)
			output[i+1] = float64(x1 - m1)
			m0 = coef*x0 + verySmall + coef2*m0
			m1 = coef*x1 + verySmall + coef2*m1
		}
		e.hpMem[0] = float64(m0)
		e.hpMem[1] = float64(m1)
	}
}

// ApplyDCReject applies a DC rejection (high-pass) filter to remove DC offset.
// This matches libopus dc_reject() which is applied before CELT encoding.
//
// The filter is a simple first-order high-pass:
//
//	coef = 6.3 * cutoffHz / sampleRate
//	out[i] = x[i] - m
//	m = coef*x[i] + (1-coef)*m
//
// Reference: libopus src/opus_encoder.c dc_reject()
func (e *Encoder) ApplyDCReject(pcm []float64) []float64 {
	if len(pcm) == 0 {
		return nil
	}

	// Initialize hpMem if not already done
	if len(e.hpMem) < e.channels {
		e.hpMem = make([]float64, e.channels)
	}

	output := make([]float64, len(pcm))
	e.applyDCRejectCore(pcm, output)
	return output
}

// applyDCRejectScratch applies DC rejection using pre-allocated scratch buffer.
// This avoids heap allocations in the hot path.
func (e *Encoder) applyDCRejectScratch(pcm []float64) []float64 {
	if len(pcm) == 0 {
		return nil
	}

	// Initialize hpMem if not already done
	if len(e.hpMem) < e.channels {
		e.hpMem = make([]float64, e.channels)
	}

	// Use scratch buffer instead of allocating
	output := e.scratch.dcRejected
	if len(output) < len(pcm) {
		output = make([]float64, len(pcm))
		e.scratch.dcRejected = output
	}
	output = output[:len(pcm)]

	e.applyDCRejectCore(pcm, output)
	return output
}

// applyPreemphasisWithScalingScratch applies pre-emphasis with scaling using scratch buffer.
func (e *Encoder) applyPreemphasisWithScalingScratch(pcm []float64) []float64 {
	if len(pcm) == 0 {
		return nil
	}

	// Use scratch buffer
	output := e.scratch.preemph
	if len(output) < len(pcm) {
		output = make([]float64, len(pcm))
		e.scratch.preemph = output
	}
	output = output[:len(pcm)]

	e.applyPreemphasisWithScalingCore(pcm, output)
	return output
}
