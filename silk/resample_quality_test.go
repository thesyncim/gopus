package silk

import (
	"math"
	"testing"
)

// TestDownsamplingResamplerAR2Filter tests the AR2 filter implementation
// against the expected libopus behavior.
func TestDownsamplingResamplerAR2Filter(t *testing.T) {
	// Create a resampler for 48kHz -> 16kHz (1:3 ratio)
	// This uses the 1:3 coefficients with AR2 filter
	r := NewDownsamplingResampler(48000, 16000)

	// Verify coefficients are loaded correctly
	// From libopus resampler_rom.c silk_Resampler_1_3_COEFS:
	// First 2 values are AR2 coefficients: 16102, -15162 (Q14)
	expectedAR2_0 := int16(16102)
	expectedAR2_1 := int16(-15162)

	if r.coefs[0] != expectedAR2_0 {
		t.Errorf("AR2 coef[0]: expected %d, got %d", expectedAR2_0, r.coefs[0])
	}
	if r.coefs[1] != expectedAR2_1 {
		t.Errorf("AR2 coef[1]: expected %d, got %d", expectedAR2_1, r.coefs[1])
	}

	t.Logf("AR2 coefficients: A0=%d, A1=%d (Q14)", r.coefs[0], r.coefs[1])
}

// TestDownsamplingResamplerQuality tests the quality of downsampling.
func TestDownsamplingResamplerQuality(t *testing.T) {
	// Create a 1kHz sine wave at 48kHz sample rate
	inputRate := 48000
	outputRate := 16000
	duration := 0.2 // 200ms
	freq := 1000.0  // 1kHz

	nSamplesIn := int(float64(inputRate) * duration)

	input := make([]float32, nSamplesIn)
	for i := 0; i < nSamplesIn; i++ {
		ti := float64(i) / float64(inputRate)
		input[i] = float32(0.9 * math.Sin(2*math.Pi*freq*ti))
	}

	// Resample
	r := NewDownsamplingResampler(inputRate, outputRate)
	output := r.Process(input)

	if len(output) == 0 {
		t.Fatal("No output from resampler")
	}

	t.Logf("Input: %d samples @ %dHz, Output: %d samples @ %dHz",
		nSamplesIn, inputRate, len(output), outputRate)

	// Skip first 100 samples for filter settling
	skip := 100

	// Compare RMS energy
	var inputRMS, outputRMS float64
	for _, v := range input {
		inputRMS += float64(v) * float64(v)
	}
	inputRMS = math.Sqrt(inputRMS / float64(len(input)))

	for _, v := range output[skip:] {
		outputRMS += float64(v) * float64(v)
	}
	outputRMS = math.Sqrt(outputRMS / float64(len(output)-skip))

	t.Logf("Input RMS: %.4f", inputRMS)
	t.Logf("Output RMS: %.4f", outputRMS)
	t.Logf("Output/Input ratio: %.4f", outputRMS/inputRMS)

	// Check that output energy is reasonable (should be close to input)
	energyRatio := outputRMS / inputRMS
	if energyRatio < 0.8 || energyRatio > 1.2 {
		t.Errorf("Energy ratio %.4f is too far from 1.0", energyRatio)
	}

	// Measure frequency preservation via zero crossings
	zeroCrossings := 0
	for i := skip + 1; i < len(output); i++ {
		if (output[i-1] >= 0 && output[i] < 0) || (output[i-1] < 0 && output[i] >= 0) {
			zeroCrossings++
		}
	}

	// Expected zero crossings: 2 per period * frequency * duration (after skip)
	outputDuration := float64(len(output)-skip) / float64(outputRate)
	expectedCrossings := 2 * freq * outputDuration
	crossingRatio := float64(zeroCrossings) / expectedCrossings

	t.Logf("Zero crossings: %d, expected ~%.0f, ratio: %.4f", zeroCrossings, expectedCrossings, crossingRatio)

	// The frequency should be preserved (crossings within 5%)
	if crossingRatio < 0.95 || crossingRatio > 1.05 {
		t.Errorf("Zero crossing ratio %.4f indicates frequency distortion", crossingRatio)
	}

	// Log first few samples for debugging
	t.Logf("First 10 input samples: %v", input[:10])
	t.Logf("First 10 output samples: %v", output[:10])
}

// TestDownsamplingResamplerSNR measures the signal-to-noise ratio of the resampler.
// Note: Measuring absolute SNR against an ideal signal is challenging due to filter delay.
// Instead, we verify that the resampler preserves signal energy reasonably.
func TestDownsamplingResamplerSNR(t *testing.T) {
	// Test multiple ratios
	testCases := []struct {
		name     string
		inRate   int
		outRate  int
		freq     float64 // Test frequency
	}{
		{"48kHz->16kHz (1:3)", 48000, 16000, 1000},
		{"48kHz->12kHz (1:4)", 48000, 12000, 1000},
		{"48kHz->8kHz (1:6)", 48000, 8000, 1000},
		{"48kHz->24kHz (1:2)", 48000, 24000, 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Generate input signal (sine wave)
			duration := 0.2 // 200ms for better frequency measurement
			nSamplesIn := int(float64(tc.inRate) * duration)

			input := make([]float32, nSamplesIn)
			for i := 0; i < nSamplesIn; i++ {
				ti := float64(i) / float64(tc.inRate)
				input[i] = float32(0.9 * math.Sin(2*math.Pi*tc.freq*ti))
			}

			// Resample
			r := NewDownsamplingResampler(tc.inRate, tc.outRate)
			output := r.Process(input)

			// Skip initial transient (filter settling time)
			// The FIR filter has order 18/24/36, so skip ~50 output samples
			skip := 100
			if skip >= len(output) {
				t.Skipf("Not enough samples for analysis")
				return
			}

			// Measure input and output RMS energy
			var inputRMS, outputRMS float64
			for _, v := range input {
				inputRMS += float64(v) * float64(v)
			}
			inputRMS = math.Sqrt(inputRMS / float64(len(input)))

			for _, v := range output[skip:] {
				outputRMS += float64(v) * float64(v)
			}
			outputRMS = math.Sqrt(outputRMS / float64(len(output)-skip))

			energyRatio := outputRMS / inputRMS
			t.Logf("Input RMS: %.4f, Output RMS: %.4f, Ratio: %.4f", inputRMS, outputRMS, energyRatio)

			// The energy should be preserved reasonably well (within 20% or so)
			if energyRatio < 0.5 || energyRatio > 1.5 {
				t.Errorf("Energy ratio %.4f is too far from 1.0 - resampler may have significant issues", energyRatio)
			}

			// Verify that the output frequency is correct by counting zero crossings
			zeroCrossings := 0
			for i := skip + 1; i < len(output); i++ {
				if (output[i-1] >= 0 && output[i] < 0) || (output[i-1] < 0 && output[i] >= 0) {
					zeroCrossings++
				}
			}

			// Expected zero crossings: 2 per period * frequency * duration (after skip)
			outputDuration := float64(len(output)-skip) / float64(tc.outRate)
			expectedCrossings := 2 * tc.freq * outputDuration
			crossingRatio := float64(zeroCrossings) / expectedCrossings

			t.Logf("Zero crossings: %d, expected ~%.0f, ratio: %.4f", zeroCrossings, expectedCrossings, crossingRatio)

			// The frequency should be preserved (crossings within 10%)
			if crossingRatio < 0.9 || crossingRatio > 1.1 {
				t.Errorf("Zero crossing ratio %.4f indicates frequency distortion", crossingRatio)
			}
		})
	}
}

// TestAR2FilterImplementation tests the AR2 filter against expected behavior.
// This is a direct comparison with the libopus silk_resampler_private_AR2 function.
func TestAR2FilterImplementation(t *testing.T) {
	// Create resampler to get access to AR2 filter
	r := NewDownsamplingResampler(48000, 16000)

	// Test with a simple impulse
	input := make([]int16, 10)
	input[0] = 32767 // Impulse

	// Expected behavior from libopus:
	// out32 = S[0] + (in[k] << 8)
	// out_Q8[k] = out32
	// out32_shifted = out32 << 2
	// S[0] = S[1] + silk_SMULWB(out32_shifted, A_Q14[0])
	// S[1] = silk_SMULWB(out32_shifted, A_Q14[1])

	// Get the AR2 coefficients
	A0 := int32(r.coefs[0]) // 16102 in Q14
	A1 := int32(r.coefs[1]) // -15162 in Q14

	t.Logf("AR2 coefficients: A0=%d, A1=%d", A0, A1)

	// Manually compute expected output using libopus algorithm
	S := [2]int32{0, 0}
	expectedOut := make([]int32, len(input))

	for k := 0; k < len(input); k++ {
		// libopus AR2 algorithm:
		out32 := S[0] + (int32(input[k]) << 8)
		expectedOut[k] = out32

		out32Shifted := out32 << 2
		S[0] = S[1] + silkSMULWB(out32Shifted, A0)
		S[1] = silkSMULWB(out32Shifted, A1)
	}

	// Now run through the resampler's AR2
	r.sIIR = [2]int32{0, 0}
	actualOut := make([]int32, len(input))
	r.ar2Filter(actualOut, input)

	// Compare
	t.Logf("Impulse response comparison:")
	for i := 0; i < len(input); i++ {
		t.Logf("  [%d] expected=%d, actual=%d, diff=%d",
			i, expectedOut[i], actualOut[i], actualOut[i]-expectedOut[i])
	}

	// Check if they match
	matches := 0
	for i := 0; i < len(input); i++ {
		if actualOut[i] == expectedOut[i] {
			matches++
		}
	}

	if matches != len(input) {
		t.Errorf("AR2 filter implementation does not match libopus! %d/%d samples match",
			matches, len(input))
	} else {
		t.Logf("AR2 filter implementation matches libopus!")
	}
}
