package testvectors

import (
	"math"
)

// Quality metric constants based on RFC 8251 and opus_compare.c
const (
	// QualityThreshold is the minimum Q value for passing compliance tests.
	// Q >= 0 corresponds to approximately 48 dB SNR.
	QualityThreshold = 0.0

	// TargetSNR is the reference SNR in dB that maps to Q=0.
	// This is derived from the opus_compare quality formula.
	TargetSNR = 48.0

	// QualityScale is the scaling factor to normalize SNR to Q range.
	// Q = (SNR - TargetSNR) * (100 / TargetSNR)
	// This gives Q=0 at 48dB, Q=100 at 96dB, Q=-50 at 24dB
	QualityScale = 100.0 / TargetSNR
)

// ComputeQuality computes a quality metric between decoded and reference audio.
//
// This implements a simplified SNR-based comparison. The full opus_compare
// uses psychoacoustic masking across 21 frequency bands, but for most decoder
// compliance testing, this SNR-based metric is sufficient.
//
// Parameters:
//   - decoded: Decoded PCM samples from gopus decoder
//   - reference: Reference PCM samples from .dec file
//   - sampleRate: Sample rate in Hz (affects nothing currently, reserved for future)
//
// Returns: Q value where Q >= 0 indicates passing (48 dB SNR threshold)
//
// The quality formula maps SNR to a Q scale:
//   - Q = 0: 48 dB SNR (pass threshold)
//   - Q = 50: 72 dB SNR (excellent)
//   - Q = 100: 96 dB SNR (near-perfect)
//   - Q = -50: 24 dB SNR (poor)
func ComputeQuality(decoded, reference []int16, sampleRate int) float64 {
	if len(decoded) == 0 || len(reference) == 0 {
		return math.Inf(-1) // No samples to compare
	}

	// Use shorter length if mismatched
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}

	// Compute signal power and noise power
	var signalPower, noisePower float64

	for i := 0; i < n; i++ {
		ref := float64(reference[i])
		dec := float64(decoded[i])

		signalPower += ref * ref
		noise := dec - ref
		noisePower += noise * noise
	}

	// Normalize by sample count
	signalPower /= float64(n)
	noisePower /= float64(n)

	// Handle edge cases
	if signalPower == 0 {
		// Silent reference - check if decoded is also silent
		if noisePower == 0 {
			return 100.0 // Both silent = perfect match
		}
		return math.Inf(-1) // Noise against silence = bad
	}

	if noisePower == 0 {
		return 100.0 // Perfect match (no noise)
	}

	// Compute SNR in dB
	snr := 10.0 * math.Log10(signalPower/noisePower)

	// Map SNR to Q scale
	// Q = (SNR - TargetSNR) * QualityScale
	// This gives Q=0 at 48dB SNR
	q := (snr - TargetSNR) * QualityScale

	return q
}

// QualityPasses returns true if the quality metric meets RFC 8251 threshold.
// A Q value >= 0 indicates the decoder output is within acceptable tolerance
// of the reference (approximately 48 dB SNR).
func QualityPasses(q float64) bool {
	return q >= QualityThreshold
}

// CompareSamples computes the mean squared error (MSE) between two sample slices.
// Returns MSE as a float64 value. Lower values indicate better match.
//
// If lengths differ, comparison uses the shorter length.
func CompareSamples(a, b []int16) float64 {
	if len(a) == 0 || len(b) == 0 {
		return math.Inf(1)
	}

	n := len(a)
	if len(b) < n {
		n = len(b)
	}

	var mse float64
	for i := 0; i < n; i++ {
		diff := float64(a[i]) - float64(b[i])
		mse += diff * diff
	}

	return mse / float64(n)
}

// NormalizedSNR computes signal-to-noise ratio in dB.
// Signal is the reference audio, noise is computed as (decoded - reference).
//
// Returns SNR in dB, or -Inf if signal is silent, or +Inf if noise is zero.
func NormalizedSNR(signal, noise []int16) float64 {
	if len(signal) == 0 || len(noise) == 0 {
		return math.Inf(-1)
	}

	n := len(signal)
	if len(noise) < n {
		n = len(noise)
	}

	var signalPower, noisePower float64

	for i := 0; i < n; i++ {
		s := float64(signal[i])
		e := float64(noise[i])

		signalPower += s * s
		noisePower += e * e
	}

	if signalPower == 0 {
		return math.Inf(-1) // Silent signal
	}
	if noisePower == 0 {
		return math.Inf(1) // No noise
	}

	return 10.0 * math.Log10(signalPower / noisePower)
}

// ComputeNoiseVector computes the difference between decoded and reference samples.
// noise[i] = decoded[i] - reference[i]
//
// Returns noise vector of the same length as the shorter input.
func ComputeNoiseVector(decoded, reference []int16) []int16 {
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}

	noise := make([]int16, n)
	for i := 0; i < n; i++ {
		// Clamp to int16 range to avoid overflow
		diff := int32(decoded[i]) - int32(reference[i])
		if diff > 32767 {
			diff = 32767
		} else if diff < -32768 {
			diff = -32768
		}
		noise[i] = int16(diff)
	}

	return noise
}

// QualityFromSNR converts an SNR value (in dB) to the Q quality metric.
// This is useful when you have precomputed SNR values.
func QualityFromSNR(snrDB float64) float64 {
	return (snrDB - TargetSNR) * QualityScale
}

// SNRFromQuality converts a Q quality metric back to SNR (in dB).
func SNRFromQuality(q float64) float64 {
	return (q / QualityScale) + TargetSNR
}
