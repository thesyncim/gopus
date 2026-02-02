package silk

import (
	"math"
	"testing"
)

// TestNSQScalingMathTrace traces the exact math in NSQ scaling.
func TestNSQScalingMathTrace(t *testing.T) {
	// Input: constant signal at amplitude 16384 (0.5 in int16)
	inputAmplitude := int16(16384)

	// Gain: should be 16384 (RMS of constant signal = amplitude)
	// In Q16: 16384 * 65536 = 1073741824
	gainQ16 := int32(16384 * 65536)

	t.Logf("=== NSQ Scaling Math Trace ===")
	t.Logf("Input amplitude: %d", inputAmplitude)
	t.Logf("Gain (linear): 16384")
	t.Logf("Gain (Q16): %d", gainQ16)

	// === ENCODER SIDE ===
	t.Logf("\n=== ENCODER SIDE (scaleNSQStates) ===")

	// Step 1: Compute inverse gain
	// invGainQ31 = (1 << 47) / gainQ16
	invGainQ31 := silk_INVERSE32_varQ(gainQ16, 47)
	t.Logf("invGainQ31 = silk_INVERSE32_varQ(%d, 47) = %d", gainQ16, invGainQ31)
	t.Logf("  This should be ~131072 (1.0 in Q31 / 16384)")
	t.Logf("  Actual: %.6f * 2^31", float64(invGainQ31) / float64(1<<31))

	// Step 2: Shift to Q26
	invGainQ26 := silk_RSHIFT_ROUND(invGainQ31, 5)
	t.Logf("invGainQ26 = invGainQ31 >> 5 = %d", invGainQ26)
	t.Logf("  This is %.6f in Q26 domain", float64(invGainQ26) / float64(1<<26))

	// Step 3: Scale input
	// xScQ10 = silk_SMULWW(x16, invGainQ26)
	// silk_SMULWW(a, b) = (a * b) >> 16
	xScQ10 := silk_SMULWW(int32(inputAmplitude), invGainQ26)
	t.Logf("xScQ10 = silk_SMULWW(%d, %d) = %d", inputAmplitude, invGainQ26, xScQ10)
	t.Logf("  = (%d * %d) >> 16 = %d", inputAmplitude, invGainQ26, (int64(inputAmplitude) * int64(invGainQ26)) >> 16)
	t.Logf("  In Q10, this is %.6f", float64(xScQ10) / float64(1<<10))

	// === EXPECTED RESULT ===
	// If input = 16384 and gain = 16384, then:
	// scaled_input = input / gain = 16384 / 16384 = 1.0
	// In Q10, 1.0 = 1024
	t.Logf("Expected xScQ10 = 1024 (1.0 in Q10)")
	t.Logf("Actual xScQ10 = %d (%.6f in Q10)", xScQ10, float64(xScQ10) / 1024.0)

	// === DECODER SIDE ===
	t.Logf("\n=== DECODER SIDE ===")

	// gainQ10 = gainQ16 >> 6
	gainQ10 := gainQ16 >> 6
	t.Logf("gainQ10 = gainQ16 >> 6 = %d", gainQ10)
	t.Logf("  This is %.6f in Q10 domain", float64(gainQ10) / float64(1<<10))

	// If we had sLPCQ14 = 1.0 in Q14 = 16384, then:
	// output = silk_SMULWW(sLPCQ14, gainQ10) >> 8
	sLPCQ14 := int32(16384) // 1.0 in Q14
	outputRaw := silk_SMULWW(sLPCQ14, gainQ10)
	output := silk_RSHIFT_ROUND(outputRaw, 8)
	t.Logf("For sLPCQ14 = %d (1.0 in Q14):", sLPCQ14)
	t.Logf("  silk_SMULWW(%d, %d) = %d", sLPCQ14, gainQ10, outputRaw)
	t.Logf("  >> 8 = %d", output)
	t.Logf("Expected output ≈ 16384 (gain * 1.0)")

	// === FULL ROUNDTRIP ===
	t.Logf("\n=== FULL ROUNDTRIP (no quantization) ===")

	// Encoder scales: input -> input/gain -> xScQ10
	// Assume perfect quantization: xScQ10 stays the same
	// Convert to Q14: xScQ10 << 4
	xQ14 := int32(xScQ10) << 4
	t.Logf("xQ14 = xScQ10 << 4 = %d", xQ14)

	// Decoder scales: xQ14 * gainQ10 >> 8
	outputFromRoundtrip := silk_RSHIFT_ROUND(silk_SMULWW(xQ14, gainQ10), 8)
	t.Logf("output = silk_SMULWW(%d, %d) >> 8 = %d", xQ14, gainQ10, outputFromRoundtrip)
	t.Logf("Expected: %d", inputAmplitude)
	t.Logf("Ratio: %.6f", float64(outputFromRoundtrip) / float64(inputAmplitude))

	// === ANALYZE THE SCALING ===
	t.Logf("\n=== SCALING ANALYSIS ===")
	t.Logf("Q format analysis:")
	t.Logf("  inputQ0 = %d", inputAmplitude)
	t.Logf("  invGainQ26 = %d (should scale by 1/gain)", invGainQ26)
	t.Logf("  xScQ10 = (inputQ0 * invGainQ26) >> 16 = Q0 * Q26 >> 16 = Q10 (correct!)")
	t.Logf("  xQ14 = xScQ10 << 4 = Q14 (correct!)")
	t.Logf("  gainQ10 = gainQ16 >> 6 = Q10 (correct!)")
	t.Logf("  output = (xQ14 * gainQ10) >> 16 >> 8 = Q14 * Q10 >> 24 = Q0 (correct!)")

	// So the Q-format chain is correct. The issue must be in the actual values!
	// Let's verify the inverse gain calculation
	t.Logf("\n=== INVERSE GAIN VERIFICATION ===")
	expectedInvGainQ31 := float64(1<<47) / float64(gainQ16)
	t.Logf("Expected invGainQ31 = 2^47 / %d = %.2f", gainQ16, expectedInvGainQ31)
	t.Logf("Actual invGainQ31 = %d", invGainQ31)
	t.Logf("Ratio: %.6f", float64(invGainQ31) / expectedInvGainQ31)
}

// TestNSQScalingIssueIsolation isolates the scaling issue.
func TestNSQScalingIssueIsolation(t *testing.T) {
	// Test with a smaller gain value that doesn't overflow
	gainLinear := int32(100) // 100 in int16 domain
	gainQ16 := gainLinear << 16
	inputAmplitude := gainLinear // Same as gain for 1:1 scaling

	t.Logf("Gain (linear): %d", gainLinear)
	t.Logf("Gain (Q16): %d", gainQ16)
	t.Logf("Input amplitude: %d", inputAmplitude)

	// Encoder scaling
	invGainQ31 := silk_INVERSE32_varQ(gainQ16, 47)
	invGainQ26 := silk_RSHIFT_ROUND(invGainQ31, 5)
	xScQ10 := silk_SMULWW(inputAmplitude, invGainQ26)

	t.Logf("invGainQ31 = %d", invGainQ31)
	t.Logf("invGainQ26 = %d", invGainQ26)
	t.Logf("xScQ10 = %d (expected: 1024 for input/gain = 1.0)", xScQ10)

	// Verify xScQ10
	expectedXScQ10 := float64(inputAmplitude) / float64(gainLinear) * 1024.0
	t.Logf("Expected xScQ10 = %.2f", expectedXScQ10)

	// The issue: check silk_SMULWW
	// silk_SMULWW(a, b) = (a * b) >> 16
	// For a = 100, b = invGainQ26:
	// We want: (a * invGainQ26) >> 16 = a / gain * 2^10
	// invGainQ26 should be (2^26) / gain = (2^26) / 100 = 671088.64
	expectedInvGainQ26 := float64(1<<26) / float64(gainLinear)
	t.Logf("Expected invGainQ26 = %.2f", expectedInvGainQ26)
	t.Logf("Actual invGainQ26 = %d", invGainQ26)

	// The actual computation:
	// (100 * 671089) >> 16 = 67108900 >> 16 = 1024
	actualComputation := (int64(inputAmplitude) * int64(invGainQ26)) >> 16
	t.Logf("Actual computation: (%d * %d) >> 16 = %d", inputAmplitude, invGainQ26, actualComputation)
}

// TestGainQ16Computation tests how gains are computed from PCM.
func TestGainQ16Computation(t *testing.T) {
	// Test with known values
	amplitude := int16(16384) // 0.5 in int16
	n := 80

	// Step 1: Compute energy
	sumSq := int64(amplitude) * int64(amplitude) * int64(n)
	energyQ0 := sumSq / int64(n) // = amplitude^2 = 268435456

	t.Logf("Amplitude: %d", amplitude)
	t.Logf("Energy (amplitude^2): %d", energyQ0)

	// Step 2: Log of energy
	logEnergyQ7 := silkLin2Log(int32(energyQ0))
	t.Logf("Log energy (Q7): %d", logEnergyQ7)

	// Step 3: Divide by 2 for sqrt
	logGainQ7 := logEnergyQ7 >> 1
	t.Logf("Log gain (Q7): %d", logGainQ7)

	// Step 4: Back to linear
	gainLinear := silkLog2Lin(logGainQ7)
	t.Logf("Gain (linear): %d", gainLinear)
	t.Logf("Expected: %d (sqrt of %d)", amplitude, energyQ0)

	// Step 5: To Q16
	// The issue: gainLinear IS the gain, not a Q16 value!
	// GainQ16FromPCM does: gainQ16_64 := int64(gainLinear) << 16
	// This gives gainLinear * 65536, which is CORRECT for Q16
	gainQ16 := int64(gainLinear) << 16
	t.Logf("GainQ16 = gainLinear << 16 = %d", gainQ16)
	t.Logf("Expected GainQ16 = %d * 65536 = %d", amplitude, int64(amplitude) * 65536)

	// Verify the computation matches amplitude
	ratio := float64(gainLinear) / float64(amplitude)
	t.Logf("Gain / Amplitude ratio: %.6f (should be ~1.0)", ratio)

	if math.Abs(ratio - 1.0) > 0.01 {
		t.Logf("WARNING: Log/Lin conversion has some error")
	}
}

// TestActualNSQWithTrace runs NSQ with detailed tracing.
func TestActualNSQWithTrace(t *testing.T) {
	nsq := NewNSQState()

	// Create input signal
	inputAmplitude := int16(16384)
	subfrLength := 80
	input := make([]int16, subfrLength)
	for i := range input {
		input[i] = inputAmplitude
	}

	// Compute gain
	gainLinear := int32(16384)
	gainQ16 := gainLinear << 16 // 1073741824

	t.Logf("Input amplitude: %d", inputAmplitude)
	t.Logf("Gain (linear): %d", gainLinear)
	t.Logf("Gain (Q16): %d", gainQ16)

	// Compute expected invGain
	invGainQ31 := silk_INVERSE32_varQ(gainQ16, 47)
	invGainQ26 := silk_RSHIFT_ROUND(invGainQ31, 5)
	t.Logf("invGainQ31: %d", invGainQ31)
	t.Logf("invGainQ26: %d", invGainQ26)

	// What should xScQ10 be?
	// xScQ10 = input / gain * 2^10 = 16384 / 16384 * 1024 = 1024
	expectedXScQ10 := int32(1024)

	// What does silk_SMULWW give?
	actualXScQ10 := silk_SMULWW(int32(inputAmplitude), invGainQ26)
	t.Logf("Expected xScQ10: %d", expectedXScQ10)
	t.Logf("Actual xScQ10 = silk_SMULWW(%d, %d) = %d", inputAmplitude, invGainQ26, actualXScQ10)

	// Check the full computation
	fullComputation := (int64(inputAmplitude) * int64(invGainQ26)) >> 16
	t.Logf("Full computation: (%d * %d) >> 16 = %d", inputAmplitude, invGainQ26, fullComputation)

	// Expected invGainQ26 = 2^26 / gainLinear = 67108864 / 16384 = 4096
	expectedInvGainQ26 := int32(1 << 26) / gainLinear
	t.Logf("Expected invGainQ26: %d", expectedInvGainQ26)
	t.Logf("Ratio: %.6f", float64(invGainQ26) / float64(expectedInvGainQ26))

	// The problem might be in silk_INVERSE32_varQ
	// Let's verify: invGainQ31 = 2^47 / gainQ16 = 2^47 / (2^16 * 16384) = 2^47 / 2^30 = 2^17 = 131072
	expectedInvGainQ31 := int64(1<<47) / int64(gainQ16)
	t.Logf("Expected invGainQ31 = 2^47 / %d = %d", gainQ16, expectedInvGainQ31)
	t.Logf("Actual invGainQ31: %d", invGainQ31)
	t.Logf("Ratio: %.6f", float64(invGainQ31) / float64(expectedInvGainQ31))

	// Run actual NSQ
	params := &NSQParams{
		SignalType:       1, // Unvoiced
		QuantOffsetType:  0,
		PredCoefQ12:      make([]int16, 32),
		NLSFInterpCoefQ2: 4,
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

	pulses, xq := NoiseShapeQuantize(nsq, input, params)

	// Compute RMS
	var inputSum, xqSum float64
	for _, s := range input {
		inputSum += float64(s) * float64(s)
	}
	for _, s := range xq {
		xqSum += float64(s) * float64(s)
	}
	inputRMS := math.Sqrt(inputSum / float64(len(input)))
	xqRMS := math.Sqrt(xqSum / float64(len(xq)))

	t.Logf("\n=== NSQ Results ===")
	t.Logf("Input RMS: %.2f", inputRMS)
	t.Logf("Output RMS: %.2f", xqRMS)
	t.Logf("Ratio: %.6f", xqRMS / inputRMS)
	t.Logf("First 8 pulses: %v", pulses[:8])
	t.Logf("First 8 xq: %v", xq[:8])
}
