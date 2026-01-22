// Round-trip tests for multistream encoder/decoder validation.
// These tests verify that encoder output is correctly decodable by the decoder.
//
// The primary validation method per 09-CONTEXT.md is round-trip testing:
// encode PCM -> decode -> verify signal preservation.

package multistream

import (
	"math"
	"testing"
)

// generateTestSignal creates a multi-channel test signal with different
// frequencies per channel for channel isolation testing.
//
// Parameters:
//   - channels: number of channels to generate
//   - frameSize: samples per channel
//   - sampleRate: sample rate in Hz
//   - baseFreq: base frequency for channel 0, each channel increments by 100Hz
//
// Returns sample-interleaved output: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
func generateTestSignal(channels, frameSize, sampleRate int, baseFreq float64) []float64 {
	output := make([]float64, frameSize*channels)

	for ch := 0; ch < channels; ch++ {
		// Each channel gets a different frequency for isolation testing
		freq := baseFreq + float64(ch)*100.0

		for s := 0; s < frameSize; s++ {
			t := float64(s) / float64(sampleRate)
			// Sine wave with amplitude 0.5 to avoid clipping
			sample := 0.5 * math.Sin(2.0*math.Pi*freq*t)
			output[s*channels+ch] = sample
		}
	}

	return output
}

// generateContinuousTestSignal creates a multi-channel test signal with phase continuity
// across multiple frames for multi-frame round-trip testing.
//
// Parameters:
//   - channels: number of channels to generate
//   - frameSize: samples per channel per frame
//   - numFrames: number of consecutive frames
//   - sampleRate: sample rate in Hz
//   - baseFreq: base frequency for channel 0
//
// Returns slice of frames, each sample-interleaved.
func generateContinuousTestSignal(channels, frameSize, numFrames, sampleRate int, baseFreq float64) [][]float64 {
	frames := make([][]float64, numFrames)

	for f := 0; f < numFrames; f++ {
		frame := make([]float64, frameSize*channels)

		for ch := 0; ch < channels; ch++ {
			freq := baseFreq + float64(ch)*100.0

			for s := 0; s < frameSize; s++ {
				// Global sample index for phase continuity
				globalSample := f*frameSize + s
				t := float64(globalSample) / float64(sampleRate)
				sample := 0.5 * math.Sin(2.0*math.Pi*freq*t)
				frame[s*channels+ch] = sample
			}
		}

		frames[f] = frame
	}

	return frames
}

// computeEnergy calculates the total energy (sum of squared samples) of a signal.
// Used to verify that decoded output has audible content (not silence).
func computeEnergy(samples []float64) float64 {
	var energy float64
	for _, s := range samples {
		energy += s * s
	}
	return energy
}

// computeEnergyPerChannel calculates energy for each channel separately.
// Input format: sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
//
// Returns slice of energy values, one per channel.
func computeEnergyPerChannel(samples []float64, channels int) []float64 {
	if channels == 0 {
		return nil
	}

	frameSize := len(samples) / channels
	energies := make([]float64, channels)

	for ch := 0; ch < channels; ch++ {
		var energy float64
		for s := 0; s < frameSize; s++ {
			sample := samples[s*channels+ch]
			energy += sample * sample
		}
		energies[ch] = energy
	}

	return energies
}

// computeCorrelation computes the normalized cross-correlation between two signals.
// Returns a value between -1 and 1, where 1 indicates perfect correlation.
//
// This is useful for verifying signal quality preservation, though lossy
// compression will naturally reduce correlation somewhat.
func computeCorrelation(signal1, signal2 []float64) float64 {
	if len(signal1) != len(signal2) || len(signal1) == 0 {
		return 0
	}

	// Compute means
	var mean1, mean2 float64
	for i := 0; i < len(signal1); i++ {
		mean1 += signal1[i]
		mean2 += signal2[i]
	}
	mean1 /= float64(len(signal1))
	mean2 /= float64(len(signal2))

	// Compute correlation
	var numerator, denom1, denom2 float64
	for i := 0; i < len(signal1); i++ {
		d1 := signal1[i] - mean1
		d2 := signal2[i] - mean2
		numerator += d1 * d2
		denom1 += d1 * d1
		denom2 += d2 * d2
	}

	denominator := math.Sqrt(denom1 * denom2)
	if denominator == 0 {
		return 0
	}

	return numerator / denominator
}

// energyRatio computes the ratio of output energy to input energy.
// Used to verify that round-trip preserves signal (output energy > 0).
// Returns 0 if input energy is 0 to avoid division by zero.
func energyRatio(inputEnergy, outputEnergy float64) float64 {
	if inputEnergy == 0 {
		return 0
	}
	return outputEnergy / inputEnergy
}

// Test infrastructure verification
func TestRoundTrip_Helpers(t *testing.T) {
	t.Run("generateTestSignal", func(t *testing.T) {
		channels := 2
		frameSize := 960
		sampleRate := 48000
		baseFreq := 440.0

		signal := generateTestSignal(channels, frameSize, sampleRate, baseFreq)

		if len(signal) != channels*frameSize {
			t.Errorf("expected length %d, got %d", channels*frameSize, len(signal))
		}

		// Verify signal has energy
		energy := computeEnergy(signal)
		if energy < 0.001 {
			t.Errorf("expected signal to have energy, got %f", energy)
		}
	})

	t.Run("computeEnergyPerChannel", func(t *testing.T) {
		channels := 2
		frameSize := 100
		samples := make([]float64, channels*frameSize)

		// Channel 0: all 0.5
		// Channel 1: all 0.0
		for s := 0; s < frameSize; s++ {
			samples[s*channels+0] = 0.5
			samples[s*channels+1] = 0.0
		}

		energies := computeEnergyPerChannel(samples, channels)

		if len(energies) != channels {
			t.Errorf("expected %d energies, got %d", channels, len(energies))
		}

		// Channel 0 should have energy 0.5^2 * 100 = 25
		expectedCh0 := 0.25 * float64(frameSize)
		if math.Abs(energies[0]-expectedCh0) > 0.001 {
			t.Errorf("channel 0 energy: expected %f, got %f", expectedCh0, energies[0])
		}

		// Channel 1 should have energy 0
		if energies[1] != 0 {
			t.Errorf("channel 1 energy: expected 0, got %f", energies[1])
		}
	})

	t.Run("computeCorrelation", func(t *testing.T) {
		// Perfect correlation (identical signals)
		sig1 := []float64{1, 2, 3, 4, 5}
		corr := computeCorrelation(sig1, sig1)
		if math.Abs(corr-1.0) > 0.0001 {
			t.Errorf("expected correlation 1.0 for identical signals, got %f", corr)
		}

		// Perfect negative correlation
		sig2 := []float64{-1, -2, -3, -4, -5}
		corr = computeCorrelation(sig1, sig2)
		if math.Abs(corr-(-1.0)) > 0.0001 {
			t.Errorf("expected correlation -1.0 for inverted signals, got %f", corr)
		}
	})
}
