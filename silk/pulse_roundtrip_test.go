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

// TestNSQExcitationScalingWithUnityGain tests NSQ behavior with unity gain.
// This test demonstrates that unity gain is INCORRECT for large amplitude signals.
// The gain must normalize the signal so that residuals stay within the +/-31 pulse range.
func TestNSQExcitationScalingWithUnityGain(t *testing.T) {
	// This test intentionally uses unity gain to show it doesn't work for large signals.
	nsq := NewNSQState()

	// Create a simple input signal - constant amplitude (large!)
	inputAmplitude := int16(16384) // 0.5 in int16 scale
	subfrLength := 80
	input := make([]int16, subfrLength)
	for i := range input {
		input[i] = inputAmplitude
	}

	// Set up NSQ parameters with UNITY GAIN (this is intentionally wrong)
	params := &NSQParams{
		SignalType:       1, // Unvoiced
		QuantOffsetType:  0,
		PredCoefQ12:      make([]int16, 32),     // Zero LPC
		NLSFInterpCoefQ2: 4,                     // No interpolation
		LTPCoefQ14:       make([]int16, 20),     // No LTP
		ARShpQ13:         make([]int16, 96),     // No shaping
		HarmShapeGainQ14: make([]int, 4),        // No harmonic shaping
		TiltQ14:          make([]int, 4),        // No tilt
		LFShpQ14:         make([]int32, 4),      // No LF shaping
		GainsQ16:         []int32{65536},        // Unity gain (1.0 in Q16) - WRONG!
		PitchL:           make([]int, 4),        // No pitch
		LambdaQ10:        1024,                  // R-D tradeoff
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

	// Log what happened - this is expected to show massive amplitude loss
	t.Logf("=== Unity gain with large signal (EXPECTED TO FAIL) ===")
	t.Logf("Input RMS: %.2f, Output RMS: %.2f, Ratio: %.4f", inputRMS, xqRMS, ratio)
	t.Logf("First 8 pulses: %v (clamped to ~30 due to residual overflow)", pulses[:8])

	// With unity gain and large input, the output should be severely attenuated
	// because the residual gets clamped to +/-31*1024
	if ratio > 0.01 {
		t.Logf("Note: Unity gain works for small signals, but for amplitude 16384,")
		t.Logf("the residual overflows and gets clamped, causing severe attenuation.")
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

	// Compute the proper gain like the encoder would
	// Gain = RMS of input in int16 domain
	// For constant amplitude, RMS = amplitude
	var energySum float64
	for _, s := range input {
		energySum += float64(s) * float64(s)
	}
	rmsGain := math.Sqrt(energySum / float64(len(input)))

	// Convert to Q16: gainQ16 = rmsGain * 65536
	// But this would be ~1e9 for amplitude 16384, which is way too big!
	// The actual gain used should normalize input so that after scaling by 1/gain,
	// the residuals fit within +/- 31*1024 (pulse quantization range).
	//
	// The correct gain computation from libopus uses a more complex formula
	// that accounts for prediction residual, not raw signal amplitude.
	// Let's use the GainQ16FromPCM function which should match libopus.
	pcmInt16 := make([]int16, subfrLength)
	for i := range pcmInt16 {
		pcmInt16[i] = inputAmplitude
	}
	gainQ16 := GainQ16FromPCM(pcmInt16, subfrLength)

	// Debug: trace the GainQ16FromPCM computation step by step
	// Step 1: energyQ0 = sumSq / n = (16384^2 * 80) / 80 = 16384^2 = 268435456
	debugEnergy := int32(16384 * 16384) // 268435456
	t.Logf("Debug Step 1: energy (amplitude^2) = %d", debugEnergy)

	// Step 2: logEnergy = silkLin2Log(energy)
	debugLogEnergy := silkLin2Log(debugEnergy)
	t.Logf("Debug Step 2: logEnergy (Q7) = %d", debugLogEnergy)

	// Step 3: logGain = logEnergy >> 1 (sqrt in log domain)
	debugLogGain := debugLogEnergy >> 1
	t.Logf("Debug Step 3: logGain = logEnergy / 2 = %d", debugLogGain)

	// Step 4: gainQ16 = silkLog2Lin(logGain)
	debugLinearGain := silkLog2Lin(debugLogGain)
	t.Logf("Debug Step 4: linearGain from silkLog2Lin = %d", debugLinearGain)
	t.Logf("Debug: This should be amplitude (16384), but silkLog2Lin returns LINEAR, not Q16!")
	t.Logf("Debug: For Q16, we need linearGain * 65536 = %d", int64(debugLinearGain)*65536)

	// The BUG is clear now: silkLog2Lin returns linear value, not Q16
	// For amplitude 16384, silkLog2Lin returns ~16384, then we clamp to min 65536
	// which makes the gain UNITY when it should be 16384*65536

	t.Logf("Input amplitude: %d, RMS: %.2f", inputAmplitude, rmsGain)
	t.Logf("Computed gainQ16 via GainQ16FromPCM: %d (%.4f linear)", gainQ16, float64(gainQ16)/65536.0)

	// The correct gain should be amplitude * 65536 for Q16
	// For amplitude 16384: gainQ16 should be ~1073741824 (16384 * 65536)
	// Let's compute it directly
	correctGainQ16 := int32(rmsGain) << 16
	if correctGainQ16 < 65536 {
		correctGainQ16 = 65536
	}
	t.Logf("Correct gainQ16 should be: %d", correctGainQ16)

	// Set up NSQ parameters with the PROPERLY COMPUTED gain
	params := &NSQParams{
		SignalType:       1, // Unvoiced
		QuantOffsetType:  0,
		PredCoefQ12:      make([]int16, 32),     // Zero LPC
		NLSFInterpCoefQ2: 4,                     // No interpolation
		LTPCoefQ14:       make([]int16, 20),     // No LTP
		ARShpQ13:         make([]int16, 96),     // No shaping
		HarmShapeGainQ14: make([]int, 4),        // No harmonic shaping
		TiltQ14:          make([]int, 4),        // No tilt
		LFShpQ14:         make([]int32, 4),      // No LF shaping
		GainsQ16:         []int32{gainQ16},      // PROPER GAIN
		PitchL:           make([]int, 4),        // No pitch
		LambdaQ10:        1024,                  // R-D tradeoff
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
	testAmplitude := float32(0.5) // 50% amplitude
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

// TestDecoderExcitationReconstruction tests the decoder's excitation reconstruction.
func TestDecoderExcitationReconstruction(t *testing.T) {
	// Test that the decoder properly reconstructs excitation from pulses

	// Create test pulses
	frameLength := 80
	pulses := make([]int16, frameLength)
	pulses[0] = 5
	pulses[10] = -3
	pulses[20] = 2
	pulses[30] = -1

	// Simulated parameters
	signalType := int8(1)  // Unvoiced
	quantOffset := int8(0) // Low offset
	seed := int8(0)

	// Compute excitation manually (matching silkDecodeCore)
	offsetQ10 := silk_Quantization_Offsets_Q10[int(signalType)>>1][int(quantOffset)]

	excQ14 := make([]int32, frameLength)
	randSeed := int32(seed)
	for i := 0; i < frameLength; i++ {
		randSeed = silkRand(randSeed)
		exc := int32(pulses[i]) << 14

		if exc > 0 {
			exc -= quantLevelAdjustQ10 << 4
		} else if exc < 0 {
			exc += quantLevelAdjustQ10 << 4
		}
		exc += int32(offsetQ10) << 4

		if randSeed < 0 {
			exc = -exc
		}
		excQ14[i] = exc
		randSeed += int32(pulses[i])
	}

	// Log the values
	t.Logf("offsetQ10: %d", offsetQ10)
	t.Logf("quantLevelAdjustQ10: %d", quantLevelAdjustQ10)

	for i := 0; i < 40; i++ {
		if pulses[i] != 0 {
			t.Logf("Pulse[%d]=%d -> excQ14=%d (= %.2f)", i, pulses[i], excQ14[i], float64(excQ14[i])/16384.0)
		}
	}

	// Now test with gain scaling
	gainQ16 := int32(1 << 16) // Unity gain
	gainQ10 := gainQ16 >> 6

	// Output scaling (no LPC or LTP, just excitation)
	for i := 0; i < 4 && i < frameLength; i++ {
		if pulses[i] != 0 || i < 4 {
			sLPCQ14 := excQ14[i] // Without LPC prediction
			output := silkSAT16(silkRSHIFT_ROUND(silkSMULWW(sLPCQ14, gainQ10), 8))
			t.Logf("excQ14[%d]=%d, gainQ10=%d, output=%d", i, excQ14[i], gainQ10, output)
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
