// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the pre-emphasis filter and DC rejection for encoding.

package celt

import "math"

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

const (
	maxAbsSignBit = uint64(1) << 63
	maxAbsInfBits = uint64(0x7ff0000000000000)
)

func updateMaxAbsBits(maxBits uint64, v float64) uint64 {
	bits := math.Float64bits(v) &^ maxAbsSignBit
	if bits <= maxAbsInfBits && bits > maxBits {
		return bits
	}
	return maxBits
}

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
			x := pcm[i]
			output[i] = x - state
			state = PreemphCoef * x
		}
		e.preemphState[0] = state
	} else {
		// Stereo pre-emphasis (interleaved samples)
		stateL := e.preemphState[0]
		stateR := e.preemphState[1]

		for i := 0; i < len(pcm)-1; i += 2 {
			// Left channel
			xL := pcm[i]
			output[i] = xL - stateL
			stateL = PreemphCoef * xL

			// Right channel
			xR := pcm[i+1]
			output[i+1] = xR - stateR
			stateR = PreemphCoef * xR
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
			pcm[i] = x - state
			state = PreemphCoef * x
		}
		e.preemphState[0] = state
	} else {
		// Stereo pre-emphasis (interleaved samples)
		stateL := e.preemphState[0]
		stateR := e.preemphState[1]

		for i := 0; i < len(pcm)-1; i += 2 {
			// Left channel
			xL := pcm[i]
			pcm[i] = xL - stateL
			stateL = PreemphCoef * xL

			// Right channel
			xR := pcm[i+1]
			pcm[i+1] = xR - stateR
			stateR = PreemphCoef * xR
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
			output[i] = float64(scaled - state)
			state = coef * scaled
		}
		e.preemphState[0] = float64(state)
	} else {
		// Stereo pre-emphasis (interleaved samples) with scaling
		stateL := float32(e.preemphState[0])
		stateR := float32(e.preemphState[1])

		for i := 0; i < len(pcm)-1; i += 2 {
			// Left channel
			scaledL := float32(pcm[i]) * float32(CELTSigScale)
			output[i] = float64(scaledL - stateL)
			stateL = coef * scaledL

			// Right channel
			scaledR := float32(pcm[i+1]) * float32(CELTSigScale)
			output[i+1] = float64(scaledR - stateR)
			stateR = coef * scaledR
		}

		e.preemphState[0] = float64(stateL)
		e.preemphState[1] = float64(stateR)
	}
}

func (e *Encoder) applyPreemphasisWithScalingAndSilenceCore(pcm, output []float64, frameSize, overlap int) bool {
	return e.applyPreemphasisWithScalingAndSilenceCoreF32(pcm, output, nil, frameSize, overlap)
}

func (e *Encoder) applyPreemphasisWithScalingAndSilenceCoreF32(pcm, output []float64, outputF32 []float32, frameSize, overlap int) bool {
	if frameSize <= 0 || e.channels <= 0 || len(pcm) == 0 {
		e.overlapMax = 0
		return true
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap > frameSize {
		overlap = frameSize
	}

	channels := e.channels
	total := frameSize * channels
	if total > len(pcm) {
		total = len(pcm)
	}
	if total > len(output) {
		total = len(output)
	}
	writeF32 := len(outputF32) >= total
	if total <= 0 {
		e.overlapMax = 0
		return true
	}

	split := (frameSize - overlap) * channels
	if split < 0 {
		split = 0
	}
	if split > total {
		split = total
	}

	coef := float32(PreemphCoef)
	var firstMaxBits, overlapMaxBits uint64
	if !writeF32 && channels == 1 {
		state := float32(e.preemphState[0])
		for i := 0; i < split; i++ {
			v := pcm[i]
			firstMaxBits = updateMaxAbsBits(firstMaxBits, v)
			scaled := float32(v) * float32(CELTSigScale)
			output[i] = float64(scaled - state)
			state = coef * scaled
		}
		for i := split; i < total; i++ {
			v := pcm[i]
			overlapMaxBits = updateMaxAbsBits(overlapMaxBits, v)
			scaled := float32(v) * float32(CELTSigScale)
			output[i] = float64(scaled - state)
			state = coef * scaled
		}
		e.preemphState[0] = float64(state)
	} else if !writeF32 {
		stateL := float32(e.preemphState[0])
		stateR := float32(e.preemphState[1])
		i := 0
		for ; i+1 < split; i += 2 {
			vL := pcm[i]
			vR := pcm[i+1]
			firstMaxBits = updateMaxAbsBits(firstMaxBits, vL)
			firstMaxBits = updateMaxAbsBits(firstMaxBits, vR)

			scaledL := float32(vL) * float32(CELTSigScale)
			output[i] = float64(scaledL - stateL)
			stateL = coef * scaledL

			scaledR := float32(vR) * float32(CELTSigScale)
			output[i+1] = float64(scaledR - stateR)
			stateR = coef * scaledR
		}
		for ; i+1 < total; i += 2 {
			vL := pcm[i]
			vR := pcm[i+1]
			overlapMaxBits = updateMaxAbsBits(overlapMaxBits, vL)
			overlapMaxBits = updateMaxAbsBits(overlapMaxBits, vR)

			scaledL := float32(vL) * float32(CELTSigScale)
			output[i] = float64(scaledL - stateL)
			stateL = coef * scaledL

			scaledR := float32(vR) * float32(CELTSigScale)
			output[i+1] = float64(scaledR - stateR)
			stateR = coef * scaledR
		}
		e.preemphState[0] = float64(stateL)
		e.preemphState[1] = float64(stateR)
	} else if channels == 1 {
		state := float32(e.preemphState[0])
		for i := 0; i < split; i++ {
			v := pcm[i]
			firstMaxBits = updateMaxAbsBits(firstMaxBits, v)
			scaled := float32(v) * float32(CELTSigScale)
			y := scaled - state
			output[i] = float64(y)
			if writeF32 {
				outputF32[i] = y
			}
			state = coef * scaled
		}
		for i := split; i < total; i++ {
			v := pcm[i]
			overlapMaxBits = updateMaxAbsBits(overlapMaxBits, v)
			scaled := float32(v) * float32(CELTSigScale)
			y := scaled - state
			output[i] = float64(y)
			if writeF32 {
				outputF32[i] = y
			}
			state = coef * scaled
		}
		e.preemphState[0] = float64(state)
	} else {
		stateL := float32(e.preemphState[0])
		stateR := float32(e.preemphState[1])
		i := 0
		for ; i+1 < split; i += 2 {
			vL := pcm[i]
			vR := pcm[i+1]
			firstMaxBits = updateMaxAbsBits(firstMaxBits, vL)
			firstMaxBits = updateMaxAbsBits(firstMaxBits, vR)

			scaledL := float32(vL) * float32(CELTSigScale)
			yL := scaledL - stateL
			output[i] = float64(yL)
			if writeF32 {
				outputF32[i] = yL
			}
			stateL = coef * scaledL

			scaledR := float32(vR) * float32(CELTSigScale)
			yR := scaledR - stateR
			output[i+1] = float64(yR)
			if writeF32 {
				outputF32[i+1] = yR
			}
			stateR = coef * scaledR
		}
		for ; i+1 < total; i += 2 {
			vL := pcm[i]
			vR := pcm[i+1]
			overlapMaxBits = updateMaxAbsBits(overlapMaxBits, vL)
			overlapMaxBits = updateMaxAbsBits(overlapMaxBits, vR)

			scaledL := float32(vL) * float32(CELTSigScale)
			yL := scaledL - stateL
			output[i] = float64(yL)
			if writeF32 {
				outputF32[i] = yL
			}
			stateL = coef * scaledL

			scaledR := float32(vR) * float32(CELTSigScale)
			yR := scaledR - stateR
			output[i+1] = float64(yR)
			if writeF32 {
				outputF32[i+1] = yR
			}
			stateR = coef * scaledR
		}
		e.preemphState[0] = float64(stateL)
		e.preemphState[1] = float64(stateR)
	}

	sampleMax := e.overlapMax
	firstMax := math.Float64frombits(firstMaxBits)
	if firstMax > sampleMax {
		sampleMax = firstMax
	}
	newOverlapMax := math.Float64frombits(overlapMaxBits)
	e.overlapMax = newOverlapMax
	if newOverlapMax > sampleMax {
		sampleMax = newOverlapMax
	}

	silenceThreshold := math.Ldexp(1.0, -e.lsbDepth)
	return sampleMax <= silenceThreshold
}

// ApplyPreemphasisWithScaling applies pre-emphasis with signal scaling.
// Input samples are first scaled from float range [-1.0, 1.0] to signal scale
// (multiplied by CELTSigScale = 32768), then the pre-emphasis filter is applied.
//
// This matches libopus celt_preemphasis() behavior where samples are scaled
// and filtered together. The decoder later divides back by the same scale.
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
	// Use float32 math to match libopus float path: coef = 6.3f*cutoff_Hz/Fs.
	coef := float32(6.3) * float32(DCRejectCutoffHz) / float32(e.sampleRate)
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

// ApplyPreemphasisWithScalingScratch applies pre-emphasis with scaling using
// pre-allocated scratch buffers. This is the zero-allocation version of
// ApplyPreemphasisWithScaling, suitable for use from the hybrid encoding path.
func (e *Encoder) ApplyPreemphasisWithScalingScratch(pcm []float64) []float64 {
	return e.applyPreemphasisWithScalingScratch(pcm)
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
