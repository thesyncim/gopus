// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the pre-emphasis filter for encoding.

package celt

// CELTSigScale is the internal signal scale used by CELT.
// Input samples in float range [-1.0, 1.0] are scaled up by this factor
// for internal processing, matching libopus CELT_SIG_SCALE.
const CELTSigScale = 32768.0

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

	if e.channels == 1 {
		// Mono pre-emphasis with scaling
		state := e.preemphState[0]
		for i := range pcm {
			// Scale input to signal scale and apply pre-emphasis
			scaled := pcm[i] * CELTSigScale
			output[i] = scaled - PreemphCoef*state
			state = scaled
		}
		e.preemphState[0] = state
	} else {
		// Stereo pre-emphasis (interleaved samples) with scaling
		stateL := e.preemphState[0]
		stateR := e.preemphState[1]

		for i := 0; i < len(pcm)-1; i += 2 {
			// Left channel
			scaledL := pcm[i] * CELTSigScale
			output[i] = scaledL - PreemphCoef*stateL
			stateL = scaledL

			// Right channel
			scaledR := pcm[i+1] * CELTSigScale
			output[i+1] = scaledR - PreemphCoef*stateR
			stateR = scaledR
		}

		e.preemphState[0] = stateL
		e.preemphState[1] = stateR
	}

	return output
}
