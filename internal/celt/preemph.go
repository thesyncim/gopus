// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the pre-emphasis filter for encoding.

package celt

// ApplyPreemphasis applies the pre-emphasis filter to PCM input samples.
// Pre-emphasis boosts high frequencies to improve coding efficiency.
//
// The filter equation is:
//   y[n] = x[n] - PreemphCoef * x[n-1]
//
// This is the inverse of the decoder's de-emphasis filter:
//   y[n] = x[n] + PreemphCoef * y[n-1]
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
