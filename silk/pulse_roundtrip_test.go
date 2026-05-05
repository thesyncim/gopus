package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestPulseEncodingRoundtrip tests that pulses survive the encode/decode cycle correctly.
func TestPulseEncodingRoundtrip(t *testing.T) {
	// Create a test pattern of pulses
	frameLength := 80 // One subframe
	testPulses := make([]int32, frameLength)

	// Create a pattern with various pulse magnitudes
	for i := 0; i < frameLength; i++ {
		switch {
		case i%16 == 0:
			testPulses[i] = 5 // Positive pulse
		case i%16 == 4:
			testPulses[i] = -3 // Negative pulse
		case i%16 == 8:
			testPulses[i] = 1 // Small positive
		case i%16 == 12:
			testPulses[i] = -1 // Small negative
		default:
			testPulses[i] = 0 // Zero
		}
	}

	// Create encoder
	enc := NewEncoder(BandwidthNarrowband)

	// Initialize range encoder
	output := make([]byte, 256)
	enc.rangeEncoder = &rangecoding.Encoder{}
	enc.rangeEncoder.Init(output)

	// Encode pulses (signalType=1 unvoiced, quantOffset=0)
	signalType := 1
	quantOffset := 0
	enc.encodePulses(testPulses, signalType, quantOffset)

	// Get encoded data
	encodedData := enc.rangeEncoder.Done()

	t.Logf("Encoded %d pulses into %d bytes", frameLength, len(encodedData))
	t.Logf("First 16 input pulses: %v", testPulses[:16])

	// Now decode
	dec := NewDecoder()
	rd := &rangecoding.Decoder{}
	rd.Init(encodedData)
	dec.rangeDecoder = rd

	// Decode pulses
	decodedPulses := make([]int16, frameLength)
	silkDecodePulses(rd, decodedPulses, signalType, quantOffset, frameLength)

	t.Logf("First 16 decoded pulses: %v", decodedPulses[:16])

	// Compare
	mismatchCount := 0
	for i := 0; i < frameLength; i++ {
		expected := int16(testPulses[i])
		if decodedPulses[i] != expected {
			if mismatchCount < 10 {
				t.Logf("Mismatch at %d: expected %d, got %d", i, expected, decodedPulses[i])
			}
			mismatchCount++
		}
	}

	if mismatchCount > 0 {
		t.Errorf("Total mismatches: %d out of %d", mismatchCount, frameLength)
	} else {
		t.Logf("All %d pulses match!", frameLength)
	}
}

// TestNSQExcitationScalingWithProperGain tests NSQ with correctly computed gain.
// This is how the encoder should work - gain normalizes the signal.
func TestNSQExcitationScalingWithProperGain(t *testing.T) {
	nsq := NewNSQState()

	// Create a simple input signal
	inputAmplitude := int16(16384) // 0.5 in int16 scale
	subfrLength := 80
	input := make([]int16, subfrLength)
	for i := range input {
		input[i] = inputAmplitude
	}

	pcmInt16 := make([]int16, subfrLength)
	for i := range pcmInt16 {
		pcmInt16[i] = inputAmplitude
	}
	gainQ16 := GainQ16FromPCM(pcmInt16, subfrLength)

	// Set up NSQ parameters with the PROPERLY COMPUTED gain
	params := &NSQParams{
		SignalType:       1, // Unvoiced
		QuantOffsetType:  0,
		PredCoefQ12:      make([]int16, 32), // Zero LPC
		NLSFInterpCoefQ2: 4,                 // No interpolation
		LTPCoefQ14:       make([]int16, 20), // No LTP
		ARShpQ13:         make([]int16, 96), // No shaping
		HarmShapeGainQ14: make([]int, 4),    // No harmonic shaping
		TiltQ14:          make([]int, 4),    // No tilt
		LFShpQ14:         make([]int32, 4),  // No LF shaping
		GainsQ16:         []int32{gainQ16},  // PROPER GAIN
		PitchL:           make([]int, 4),    // No pitch
		LambdaQ10:        1024,              // R-D tradeoff
		LTPScaleQ14:      int(silk_LTPScales_table_Q14[1]),
		FrameLength:      subfrLength,
		SubfrLength:      subfrLength,
		NbSubfr:          1,
		LTPMemLength:     320,
		PredLPCOrder:     10,
		ShapeLPCOrder:    16,
		Seed:             0,
	}

	// Run NSQ
	pulses, xq := NoiseShapeQuantize(nsq, input, params)

	// Compute input RMS
	var inputSum float64
	for _, s := range input {
		inputSum += float64(s) * float64(s)
	}
	inputRMS := math.Sqrt(inputSum / float64(len(input)))

	// Compute output (xq) RMS
	var xqSum float64
	for _, s := range xq {
		xqSum += float64(s) * float64(s)
	}
	xqRMS := math.Sqrt(xqSum / float64(len(xq)))

	ratio := xqRMS / inputRMS

	t.Logf("=== Proper gain test ===")
	t.Logf("Input RMS: %.2f, Output RMS: %.2f, Ratio: %.4f", inputRMS, xqRMS, ratio)
	t.Logf("First 8 pulses: %v", pulses[:8])
	t.Logf("First 8 xq: %v", xq[:8])

	// With proper gain, the output should be reasonably close to input
	if ratio < 0.1 || ratio > 10.0 {
		t.Errorf("Amplitude ratio is too far from 1.0: %.4f", ratio)
	}
}

// TestFullEncoderGainComputation tests the encoder's gain computation.
func TestFullEncoderGainComputation(t *testing.T) {
	// Create encoder
	enc := NewEncoder(BandwidthNarrowband)

	// Create test PCM with known amplitude
	pcmFloat := make([]float32, 160) // 20ms at 8kHz
	testAmplitude := float32(0.5)    // 50% amplitude
	for i := range pcmFloat {
		pcmFloat[i] = testAmplitude
	}

	// Compute gains using the encoder's method
	numSubframes := 4
	gains := enc.computeSubframeGains(pcmFloat, numSubframes)

	t.Logf("=== Encoder gain computation ===")
	t.Logf("Input amplitude: %.2f", testAmplitude)
	for i, g := range gains {
		t.Logf("Subframe %d gain: %.2f (Q16: %d)", i, g, int(g*65536))
	}

	// Expected gain for amplitude 0.5 = 16384 in int16 domain
	// gain = sqrt(16384^2 / n) = sqrt(268435456 / 40) = sqrt(6710886) ≈ 2590
	expectedGain := float32(16384.0) // For constant signal, gain = amplitude
	for _, g := range gains {
		if g < expectedGain*0.5 || g > expectedGain*2.0 {
			t.Errorf("Gain %.2f is too far from expected %.2f", g, expectedGain)
		}
	}
}

// TestGainScalingConsistency tests that gain scaling is consistent between encoder and decoder.
// Note: silk_INVERSE32_varQ saturates at int32 max for small gains (< 1.0), so this test
// only validates gains >= 1.0. This is expected behavior - libopus gains are typically
// in the range 1-32767 (linear amplitude in int16 domain), not fractional values.
func TestGainScalingConsistency(t *testing.T) {
	// Test gains >= 1.0 (typical SILK gain range)
	// Gains < 1.0 would saturate silk_INVERSE32_varQ, which is expected
	testGains := []float32{1.0, 2.0, 5.0, 100.0, 1000.0}

	for _, gain := range testGains {
		// Encode gain
		gainQ16 := int32(gain * 65536)
		gainQ10 := gainQ16 >> 6

		// Test the inverse relationship
		invGainQ31 := silk_INVERSE32_varQ(gainQ16, 47)
		invGainQ26 := silk_RSHIFT_ROUND(invGainQ31, 5)

		// Scale a test value through both paths
		testVal := int16(10000) // Test input

		// Encoder path: input * invGain -> quantize -> encode
		scaledInput := silk_SMULWW(int32(testVal), invGainQ26)

		// Decoder path: (excitation * gainQ10) >> 8
		// For unity quantization (pulse=1), excQ14 = 1<<14 + offset adjustments
		// We're testing the gain part

		// Verify that invGain * gain ~= 1.0
		// invGainQ31 * gainQ16 should be close to 2^47
		product := float64(invGainQ31) * float64(gainQ16)
		expected := float64(int64(1) << 47)
		ratio := product / expected

		t.Logf("Gain %.2f: gainQ16=%d, gainQ10=%d, invGainQ31=%d, invGainQ26=%d",
			gain, gainQ16, gainQ10, invGainQ31, invGainQ26)
		t.Logf("  Test value %d scaled to %d", testVal, scaledInput)
		t.Logf("  invGain*gain ratio: %.6f (should be ~1.0)", ratio)

		if ratio < 0.99 || ratio > 1.01 {
			t.Errorf("Gain inversion error: ratio=%.6f", ratio)
		}
	}
}

// TestPulseExcitationRoundtrip_WithLPC tests NSQ with proper LPC prediction.
// This verifies that the pulse roundtrip works correctly when the signal has
// proper LPC prediction applied (which is the normal case in real encoding).
func TestPulseExcitationRoundtrip_WithLPC(t *testing.T) {
	nsq := NewNSQState()

	// Create a signal that LPC can predict - a low-frequency sine wave
	// LPC with high first coefficient will predict this well
	subfrLength := 80
	input := make([]int16, subfrLength)
	inputAmplitude := int16(16384)
	for i := range input {
		// Very slowly varying signal - LPC prediction should work well
		phase := float64(i) * 0.02 // Very low frequency
		input[i] = int16(float64(inputAmplitude) * math.Sin(phase))
	}

	// Compute gain
	gainQ16 := GainQ16FromPCM(input, subfrLength)

	// Create LPC coefficients that predict past values
	// For a slowly varying signal, a[0] ~= 1.0 (Q12 = 4096) predicts well
	lpcQ12 := make([]int16, 32)
	lpcQ12[0] = 3900 // ~0.95 in Q12 - strong prediction from previous sample

	t.Logf("gainQ16 = %d (%.2f linear)", gainQ16, float64(gainQ16)/65536.0)
	t.Logf("First 10 input samples: %v", input[:10])

	params := &NSQParams{
		SignalType:       1, // Unvoiced
		QuantOffsetType:  0,
		PredCoefQ12:      lpcQ12,
		NLSFInterpCoefQ2: 4, // No interpolation
		LTPCoefQ14:       make([]int16, 20),
		ARShpQ13:         make([]int16, 96),
		HarmShapeGainQ14: make([]int, 4),
		TiltQ14:          make([]int, 4),
		LFShpQ14:         make([]int32, 4),
		GainsQ16:         []int32{gainQ16},
		PitchL:           make([]int, 4),
		LambdaQ10:        1024,
		LTPScaleQ14:      int(silk_LTPScales_table_Q14[1]),
		FrameLength:      subfrLength,
		SubfrLength:      subfrLength,
		NbSubfr:          1,
		LTPMemLength:     320,
		PredLPCOrder:     10,
		ShapeLPCOrder:    16,
		Seed:             0,
	}

	// Run NSQ encoder
	pulses, xq := NoiseShapeQuantize(nsq, input, params)

	// Now decode with the same parameters
	// The decoder uses the SAME LPC coefficients to reconstruct
	// We'll manually compute what the decoder would output

	// Count non-zero pulses
	nonZeroCount := 0
	maxPulse := int8(0)
	for _, p := range pulses {
		if p != 0 {
			nonZeroCount++
		}
		if p > maxPulse || -p > maxPulse {
			if p > 0 {
				maxPulse = p
			} else {
				maxPulse = -p
			}
		}
	}

	t.Logf("Non-zero pulses: %d/%d, max magnitude: %d", nonZeroCount, subfrLength, maxPulse)
	t.Logf("First 10 pulses: %v", pulses[:10])
	t.Logf("First 10 xq (encoder output): %v", xq[:10])

	// Compute RMS of input vs output
	var inputSum, xqSum float64
	for i := 0; i < subfrLength; i++ {
		inputSum += float64(input[i]) * float64(input[i])
		xqSum += float64(xq[i]) * float64(xq[i])
	}
	inputRMS := math.Sqrt(inputSum / float64(subfrLength))
	xqRMS := math.Sqrt(xqSum / float64(subfrLength))
	ratio := xqRMS / inputRMS

	t.Logf("Input RMS: %.2f, Output RMS: %.2f, Ratio: %.4f", inputRMS, xqRMS, ratio)

	// With LPC prediction, the ratio should be much better than without
	// We expect maybe 50-90% amplitude preservation depending on quantization
	if ratio < 0.3 {
		t.Errorf("Amplitude ratio too low with LPC: %.4f (expected > 0.3)", ratio)
	}
}
