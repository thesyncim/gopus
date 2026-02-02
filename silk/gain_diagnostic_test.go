package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestGainComputationDiagnostic traces the gain computation flow to understand
// why SILK quality isn't improving despite using residual-based gains.
func TestGainComputationDiagnostic(t *testing.T) {
	// Create encoder
	enc := NewEncoder(BandwidthWideband)

	// Create test signal - a sine wave (typical speech-like signal)
	sampleRate := 16000
	frameMs := 20
	frameSamples := sampleRate * frameMs / 1000 // 320 samples
	pcm := make([]float32, frameSamples)

	// Generate 440 Hz sine wave at 0.5 amplitude
	freq := 440.0
	amplitude := float32(0.5)
	for i := range pcm {
		pcm[i] = amplitude * float32(math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
	}

	// Step 1: Compute raw signal energy (what computeSubframeGains uses)
	var rawEnergy float64
	const pcmScale = 32768.0
	for _, s := range pcm {
		scaled := float64(s) * pcmScale
		rawEnergy += scaled * scaled
	}
	rawEnergy /= float64(len(pcm))
	rawGain := math.Sqrt(rawEnergy)

	t.Logf("=== Raw Signal Analysis ===")
	t.Logf("Frame samples: %d", frameSamples)
	t.Logf("Raw signal RMS energy: %.2f", rawEnergy)
	t.Logf("Raw signal gain (sqrt): %.2f", rawGain)

	// Step 2: Perform LPC analysis (this sets lastTotalEnergy, lastInvGain, lastNumSamples)
	lpcQ12 := enc.computeLPCFromFrame(pcm)

	t.Logf("\n=== After LPC Analysis ===")
	t.Logf("LPC order: %d", len(lpcQ12))
	t.Logf("lastTotalEnergy: %.2f", enc.lastTotalEnergy)
	t.Logf("lastInvGain: %.6f", enc.lastInvGain)
	t.Logf("lastNumSamples: %d", enc.lastNumSamples)

	// Step 3: Compute residual energy
	residualEnergy := enc.lastTotalEnergy * enc.lastInvGain
	avgResidualPerSample := residualEnergy / float64(enc.lastNumSamples)
	residualGain := math.Sqrt(avgResidualPerSample)

	t.Logf("\n=== Residual Analysis ===")
	t.Logf("Total residual energy: %.2f", residualEnergy)
	t.Logf("Avg residual per sample: %.2f", avgResidualPerSample)
	t.Logf("Residual gain (sqrt): %.2f", residualGain)

	// Step 4: Compare gains
	t.Logf("\n=== Gain Comparison ===")
	t.Logf("Raw gain:      %.2f", rawGain)
	t.Logf("Residual gain: %.2f", residualGain)
	t.Logf("Ratio (residual/raw): %.4f", residualGain/rawGain)
	t.Logf("Inverse prediction gain: %.6f", enc.lastInvGain)

	// Step 5: Get actual gains from both methods
	// IMPORTANT: Both functions use the same scratch buffer! Copy results immediately.
	numSubframes := 4
	rawGainsSlice := enc.computeSubframeGains(pcm, numSubframes)
	rawGains := make([]float32, len(rawGainsSlice))
	copy(rawGains, rawGainsSlice)

	residualGainsSlice := enc.computeSubframeGainsFromResidual(pcm, numSubframes)
	residualGains := make([]float32, len(residualGainsSlice))
	copy(residualGains, residualGainsSlice)

	t.Logf("\n=== Per-Subframe Gains ===")
	for i := 0; i < numSubframes; i++ {
		t.Logf("Subframe %d: raw=%.2f, residual=%.2f, ratio=%.4f",
			i, rawGains[i], residualGains[i], residualGains[i]/rawGains[i])
	}

	// Step 5b: Manual subframe energy calculation to verify
	t.Logf("\n=== Manual Subframe Verification ===")
	subframeSamples := len(pcm) / numSubframes
	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeSamples
		end := start + subframeSamples
		var manualEnergy float64
		for i := start; i < end; i++ {
			s := float64(pcm[i]) * pcmScale
			manualEnergy += s * s
		}
		manualEnergy /= float64(end - start)
		manualGain := math.Sqrt(manualEnergy)
		t.Logf("Subframe %d: manual energy=%.2f, manual gain=%.2f, function gain=%.2f",
			sf, manualEnergy, manualGain, rawGains[sf])
	}

	// Step 6: Check if residual gains are significantly different
	// For a predictable signal like a sine wave, invGain should be small (< 0.1)
	// because LPC can predict the signal well
	if enc.lastInvGain > 0.5 {
		t.Logf("\nWARNING: Inverse prediction gain is high (%.4f), suggesting poor LPC prediction", enc.lastInvGain)
		t.Logf("Expected: < 0.1 for predictable signals like sine waves")
	}

	// Step 7: Check what happens during actual encoding
	t.Logf("\n=== Actual Encode Test ===")

	// Reset encoder state
	enc2 := NewEncoder(BandwidthWideband)
	encoded := enc2.EncodeFrame(pcm, true)

	t.Logf("Encoded frame size: %d bytes", len(encoded))
	t.Logf("After encode - lastTotalEnergy: %.2f", enc2.lastTotalEnergy)
	t.Logf("After encode - lastInvGain: %.6f", enc2.lastInvGain)
}

// TestGainComputationWithConstantSignal tests gain computation with a constant signal
// where LPC should predict perfectly (invGain should be very small).
func TestGainComputationWithConstantSignal(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Constant signal - LPC should predict this perfectly
	frameSamples := 320
	pcm := make([]float32, frameSamples)
	amplitude := float32(0.5)
	for i := range pcm {
		pcm[i] = amplitude
	}

	// Compute LPC
	lpcQ12 := enc.computeLPCFromFrame(pcm)
	_ = lpcQ12

	t.Logf("=== Constant Signal Analysis ===")
	t.Logf("lastTotalEnergy: %.2f", enc.lastTotalEnergy)
	t.Logf("lastInvGain: %.6f", enc.lastInvGain)
	t.Logf("lastNumSamples: %d", enc.lastNumSamples)

	// For a constant signal, invGain should be very small (near 0)
	// because LPC can predict it perfectly with just the first coefficient
	if enc.lastInvGain > 0.01 {
		t.Logf("NOTE: invGain is %.6f for constant signal", enc.lastInvGain)
		t.Logf("This suggests the Burg analysis may not be computing invGain correctly")
	}

	// Compare gains
	numSubframes := 4
	rawGains := enc.computeSubframeGains(pcm, numSubframes)
	residualGains := enc.computeSubframeGainsFromResidual(pcm, numSubframes)

	t.Logf("\n=== Gain Comparison ===")
	for i := 0; i < numSubframes; i++ {
		t.Logf("Subframe %d: raw=%.2f, residual=%.2f", i, rawGains[i], residualGains[i])
	}
}

// TestEncoderGainTrace traces actual gains during encoding.
func TestEncoderGainTrace(t *testing.T) {
	// Create test signal - 440 Hz sine at 0.5 amplitude
	sampleRate := 16000
	frameSamples := 320 // 20ms
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		pcm[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
	}

	// Create encoder
	enc := NewEncoder(BandwidthWideband)

	// Step 1: Compute LPC (this sets lastTotalEnergy, lastInvGain)
	lpcQ12 := enc.computeLPCFromFrame(pcm)
	_ = lpcQ12

	t.Logf("After LPC:")
	t.Logf("  totalEnergy: %.2f", enc.lastTotalEnergy)
	t.Logf("  invGain: %.6f", enc.lastInvGain)

	// Step 2: Compute residual gains
	numSubframes := 4
	gains := enc.computeSubframeGainsFromResidual(pcm, numSubframes)

	// Copy to avoid aliasing
	gainsCopy := make([]float32, len(gains))
	copy(gainsCopy, gains)

	t.Logf("\nResidual gains (float):")
	for i, g := range gainsCopy {
		t.Logf("  Subframe %d: %.2f", i, g)
	}

	// Step 3: Compare raw vs residual gains (skip encoding which needs range encoder)
	rawGains := enc.computeSubframeGains(pcm, numSubframes)
	rawGainsCopy := make([]float32, len(rawGains))
	copy(rawGainsCopy, rawGains)

	t.Logf("\nComparison (raw vs residual):")
	for i := 0; i < numSubframes; i++ {
		t.Logf("  Subframe %d: raw=%.2f, residual=%.2f, ratio=%.4f",
			i, rawGainsCopy[i], gainsCopy[i], gainsCopy[i]/rawGainsCopy[i])
	}

	// Step 4: Do a full encode to verify the complete flow
	t.Logf("\n=== Full Encode Test ===")
	enc2 := NewEncoder(BandwidthWideband)
	encoded := enc2.EncodeFrame(pcm, true)
	t.Logf("Encoded frame: %d bytes", len(encoded))

	// Check the gains that were used (stored in encoder state during encode)
	t.Logf("During encode - invGain used: %.6f", enc2.lastInvGain)
}

// TestSILKRoundtripQuality tests the full SILK encode/decode roundtrip.
func TestSILKRoundtripQuality(t *testing.T) {
	// Create a simple test signal
	frameSamples := 320 // 20ms at 16kHz

	// Use a simple sine wave
	pcmFloat := make([]float32, frameSamples)
	amplitude := float32(0.3)
	for i := range pcmFloat {
		pcmFloat[i] = amplitude * float32(math.Sin(2*math.Pi*440*float64(i)/16000))
	}

	// Encode
	enc := NewEncoder(BandwidthWideband)
	encoded := enc.EncodeFrame(pcmFloat, true)
	t.Logf("Encoded %d samples into %d bytes", frameSamples, len(encoded))

	// Decode using SILK decoder - use DecodeFrameRaw to get native samples
	dec := NewDecoder()
	rd := &rangecoding.Decoder{}
	rd.Init(encoded)

	nativeSamples, err := dec.DecodeFrameRaw(rd, BandwidthWideband, Frame20ms, true)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	t.Logf("Decoded %d native samples at 16kHz", len(nativeSamples))

	// Check decoded indices
	st := &dec.state[0]
	t.Logf("Decoded signalType: %d", st.indices.signalType)
	t.Logf("Decoded quantOffset: %d", st.indices.quantOffsetType)
	t.Logf("Decoded GainsIndices: %v", st.indices.GainsIndices[:4])

	// Check decoded gain values
	t.Logf("Decoded prevGainQ16: %d (linear=%.4f)", st.prevGainQ16, float64(st.prevGainQ16)/65536.0)

	// Also check encoder's gain values
	t.Logf("\n=== Encoder Gain Analysis ===")
	t.Logf("Encoder lastTotalEnergy: %.2f", enc.lastTotalEnergy)
	t.Logf("Encoder lastInvGain: %.6f", enc.lastInvGain)

	// Compute what gains should be
	numSubframes := 4
	residualGains := enc.computeSubframeGainsFromResidual(pcmFloat, numSubframes)
	residualGainsCopy := make([]float32, len(residualGains))
	copy(residualGainsCopy, residualGains)

	rawGains := enc.computeSubframeGains(pcmFloat, numSubframes)
	rawGainsCopy := make([]float32, len(rawGains))
	copy(rawGainsCopy, rawGains)

	t.Logf("Residual gains (float): %v", residualGainsCopy)
	t.Logf("Raw gains (float): %v", rawGainsCopy)

	// The encoded gain index 25 with prevIndex=0 should give:
	// Linear gain = silk_log2lin(invScaleQ16 * 25 + offset)
	// Let's compute this
	invScaleQ16 := int32(2251) // From libopus constants
	offset := int32(2090)      // From libopus constants
	logQ7_25 := silkSMULWB(invScaleQ16, 25) + offset
	linearGain25 := silkLog2Lin(logQ7_25)
	t.Logf("\nGain index 25 -> logQ7=%d -> linearGain=%d", logQ7_25, linearGain25)

	pcmOut := nativeSamples

	// Compare (pcmOut is float32 in range [-1, 1])
	var sumSqInput, sumSqOutput, sumSqDiff float64
	for i := 0; i < frameSamples && i < len(pcmOut); i++ {
		inVal := float64(pcmFloat[i])
		outVal := float64(pcmOut[i])
		diff := inVal - outVal
		sumSqInput += inVal * inVal
		sumSqOutput += outVal * outVal
		sumSqDiff += diff * diff
	}

	inputRMS := math.Sqrt(sumSqInput / float64(frameSamples))
	outputRMS := math.Sqrt(sumSqOutput / float64(frameSamples))
	diffRMS := math.Sqrt(sumSqDiff / float64(frameSamples))

	snr := 10 * math.Log10(sumSqInput/sumSqDiff)

	t.Logf("Input RMS: %.4f", inputRMS)
	t.Logf("Output RMS: %.4f", outputRMS)
	t.Logf("Diff RMS: %.4f", diffRMS)
	t.Logf("SNR: %.2f dB", snr)
	t.Logf("Amplitude ratio (out/in): %.4f", outputRMS/inputRMS)

	// Log first few samples
	t.Logf("\nFirst 10 samples:")
	for i := 0; i < 10 && i < frameSamples && i < len(pcmOut); i++ {
		t.Logf("  [%d] in=%.4f, out=%.4f, diff=%.4f", i, pcmFloat[i], pcmOut[i], pcmFloat[i]-pcmOut[i])
	}
}

// TestInvGainComputation directly tests if invGain is computed correctly.
func TestInvGainComputation(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Test with different signal types
	testCases := []struct {
		name        string
		makeSignal  func(n int) []float32
		expectLowInvGain bool // True if signal is highly predictable
	}{
		{
			name: "constant",
			makeSignal: func(n int) []float32 {
				pcm := make([]float32, n)
				for i := range pcm {
					pcm[i] = 0.5
				}
				return pcm
			},
			expectLowInvGain: true,
		},
		{
			name: "sine_440Hz",
			makeSignal: func(n int) []float32 {
				pcm := make([]float32, n)
				for i := range pcm {
					pcm[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/16000))
				}
				return pcm
			},
			expectLowInvGain: true, // Sine is very predictable
		},
		{
			name: "white_noise",
			makeSignal: func(n int) []float32 {
				pcm := make([]float32, n)
				seed := int32(12345)
				for i := range pcm {
					seed = seed*1103515245 + 12345
					pcm[i] = float32(seed) / float32(1<<31) * 0.5
				}
				return pcm
			},
			expectLowInvGain: false, // Noise is unpredictable
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := tc.makeSignal(320)
			enc.computeLPCFromFrame(pcm)

			t.Logf("Signal: %s", tc.name)
			t.Logf("  totalEnergy: %.2f", enc.lastTotalEnergy)
			t.Logf("  invGain: %.6f", enc.lastInvGain)
			t.Logf("  numSamples: %d", enc.lastNumSamples)

			if tc.expectLowInvGain && enc.lastInvGain > 0.3 {
				t.Errorf("Expected low invGain for %s, got %.6f", tc.name, enc.lastInvGain)
			}
			if !tc.expectLowInvGain && enc.lastInvGain < 0.3 {
				t.Logf("Note: invGain is low (%.6f) for unpredictable signal %s", enc.lastInvGain, tc.name)
			}
		})
	}
}
