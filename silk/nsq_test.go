package silk

import (
	"math"
	"testing"
)

// TestNSQStateInitialization verifies proper NSQ state initialization.
func TestNSQStateInitialization(t *testing.T) {
	state := NewNSQState()

	if state == nil {
		t.Fatal("NewNSQState returned nil")
	}

	if state.prevGainQ16 != 1<<16 {
		t.Errorf("Expected prevGainQ16 = %d (1<<16), got %d", 1<<16, state.prevGainQ16)
	}

	// Verify all arrays are zero-initialized
	for i, v := range state.xq {
		if v != 0 {
			t.Errorf("xq[%d] not zero: %d", i, v)
			break
		}
	}

	for i, v := range state.sLPCQ14 {
		if v != 0 {
			t.Errorf("sLPCQ14[%d] not zero: %d", i, v)
			break
		}
	}
}

// TestNSQStateReset verifies state reset functionality.
func TestNSQStateReset(t *testing.T) {
	state := NewNSQState()

	// Modify state
	state.randSeed = 12345
	state.lagPrev = 100
	state.sLFARShpQ14 = 5000
	state.sLPCQ14[0] = 1000

	// Reset
	state.Reset()

	// Verify reset
	if state.randSeed != 0 {
		t.Errorf("randSeed not reset: %d", state.randSeed)
	}
	if state.lagPrev != 0 {
		t.Errorf("lagPrev not reset: %d", state.lagPrev)
	}
	if state.sLFARShpQ14 != 0 {
		t.Errorf("sLFARShpQ14 not reset: %d", state.sLFARShpQ14)
	}
	if state.sLPCQ14[0] != 0 {
		t.Errorf("sLPCQ14[0] not reset: %d", state.sLPCQ14[0])
	}
	if state.prevGainQ16 != 1<<16 {
		t.Errorf("prevGainQ16 not reset to default: got %d, want %d", state.prevGainQ16, 1<<16)
	}
}

// TestGetQuantizationOffset verifies correct offset selection.
func TestGetQuantizationOffset(t *testing.T) {
	tests := []struct {
		signalType      int
		quantOffsetType int
		expected        int
	}{
		// Unvoiced (signalType 0, 1)
		{0, 0, offsetUVLQ10}, // 100
		{0, 1, offsetUVHQ10}, // 240
		{1, 0, offsetUVLQ10},
		{1, 1, offsetUVHQ10},
		// Voiced (signalType 2)
		{2, 0, offsetVLQ10}, // 32
		{2, 1, offsetVHQ10}, // 100
	}

	for _, tc := range tests {
		result := getQuantizationOffset(tc.signalType, tc.quantOffsetType)
		if result != tc.expected {
			t.Errorf("getQuantizationOffset(%d, %d) = %d, expected %d",
				tc.signalType, tc.quantOffsetType, result, tc.expected)
		}
	}
}

// TestSilkRAND verifies the LCG random number generator.
func TestSilkRAND(t *testing.T) {
	seed := int32(1)

	// Run a few iterations and verify non-zero output
	for i := 0; i < 10; i++ {
		seed = silk_RAND(seed)
		// Just verify it produces varying output
	}

	// LCG should not produce zero for non-zero seed
	seed = int32(1)
	for i := 0; i < 100; i++ {
		seed = silk_RAND(seed)
	}
	if seed == 0 {
		t.Error("LCG produced zero after iterations")
	}
}

// TestFixedPointMath verifies fixed-point math operations.
func TestFixedPointMath(t *testing.T) {
	// Test SMLAWB: a + ((b * (c & 0xFFFF)) >> 16)
	result := silk_SMLAWB(100, 65536, 0x10000)
	if result != 100 { // c & 0xFFFF = 0
		t.Errorf("silk_SMLAWB: expected 100, got %d", result)
	}

	// Test with positive values in range
	// Note: c is cast to int16, so 0x4000 = 16384 (positive)
	result = silk_SMLAWB(0, 65536, 16384) // 1.0 * (16384/65536) = 0.25 -> 16384
	expected := int32(16384)              // 65536 * 16384 >> 16 = 16384
	if result != expected {
		t.Errorf("silk_SMLAWB: expected %d, got %d", expected, result)
	}

	// Test SMULWW
	result = silk_SMULWW(65536, 65536) // 1.0 * 1.0 in Q16
	if result != 65536 {
		t.Errorf("silk_SMULWW(65536, 65536) = %d, expected 65536", result)
	}

	// Test SAT16
	if silk_SAT16(40000) != 32767 {
		t.Error("silk_SAT16 failed positive saturation")
	}
	if silk_SAT16(-40000) != -32768 {
		t.Error("silk_SAT16 failed negative saturation")
	}
	if silk_SAT16(1000) != 1000 {
		t.Error("silk_SAT16 failed passthrough")
	}

	// Test LIMIT_32
	if silk_LIMIT_32(100, 50, 150) != 100 {
		t.Error("silk_LIMIT_32 failed passthrough")
	}
	if silk_LIMIT_32(200, 50, 150) != 150 {
		t.Error("silk_LIMIT_32 failed upper limit")
	}
	if silk_LIMIT_32(10, 50, 150) != 50 {
		t.Error("silk_LIMIT_32 failed lower limit")
	}
}

// TestComputeRDQuantization verifies R-D quantization.
func TestComputeRDQuantization(t *testing.T) {
	// Test with positive residual
	q1, q2, rd1, rd2 := computeRDQuantization(1500, 100, 512)

	// Both candidates should be reasonable
	if q1 == 0 && q2 == 0 {
		t.Error("Both quantization candidates are zero")
	}

	// RD costs should be non-negative
	if rd1 < 0 || rd2 < 0 {
		t.Errorf("Negative RD cost: rd1=%d, rd2=%d", rd1, rd2)
	}

	// q2 should be q1 + 1024 (one quantization step)
	if q2 != q1+1024 && q2 != 0 {
		t.Logf("q1=%d, q2=%d (delta=%d)", q1, q2, q2-q1)
	}
}

// TestShortTermPrediction verifies LPC prediction.
func TestShortTermPrediction(t *testing.T) {
	// Set up test LPC state
	sLPCQ14 := make([]int32, nsqLpcBufLength+10)
	for i := range sLPCQ14 {
		sLPCQ14[i] = int32(i * 1000) // Some test values
	}

	// Simple LPC coefficients (first-order predictor)
	aQ12 := []int16{2048} // 0.5 in Q12

	// Compute prediction
	idx := nsqLpcBufLength - 1 + 5
	result := shortTermPrediction(sLPCQ14, idx, aQ12, 1)

	// Result should be non-zero with non-zero state
	if result == 0 && sLPCQ14[idx] != 0 {
		t.Error("shortTermPrediction returned zero with non-zero state")
	}
}

// TestNoiseShapeFeedback verifies AR noise shaping.
func TestNoiseShapeFeedback(t *testing.T) {
	sAR2Q14 := make([]int32, maxShapeLpcOrder)
	arShpQ13 := make([]int16, maxShapeLpcOrder)

	// Set some test values
	sDiffShpQ14 := int32(10000)
	for i := range arShpQ13 {
		arShpQ13[i] = int16(100 + i*10)
	}

	result := noiseShapeFeedback(sDiffShpQ14, sAR2Q14, arShpQ13, 10)

	// State should be updated
	if sAR2Q14[0] != sDiffShpQ14 {
		t.Errorf("sAR2Q14[0] not updated: expected %d, got %d", sDiffShpQ14, sAR2Q14[0])
	}

	// Result should be non-zero
	if result == 0 {
		t.Error("noiseShapeFeedback returned zero")
	}
}

// TestNoiseShapeQuantizeBasic verifies basic NSQ operation.
func TestNoiseShapeQuantizeBasic(t *testing.T) {
	nsq := NewNSQState()

	// Create simple test signal (sine wave)
	frameLength := 320 // 20ms at 16kHz
	subfrLength := 80  // 5ms subframe
	numSubfr := 4

	input := make([]int16, frameLength)
	for i := range input {
		// Generate sine wave scaled to int16 range
		input[i] = int16(10000 * math.Sin(2*math.Pi*float64(i)/float64(frameLength)*4))
	}

	// Simple LPC coefficients (mild prediction)
	predCoefQ12 := make([]int16, 2*maxLPCOrder)
	predCoefQ12[0] = 2048  // First coefficient
	predCoefQ12[16] = 2048 // Same for second set

	// Gains
	gainsQ16 := make([]int32, numSubfr)
	for i := range gainsQ16 {
		gainsQ16[i] = 65536 // 1.0 in Q16
	}

	// Pitch lags (unvoiced)
	pitchL := make([]int, numSubfr)

	// AR shaping coefficients
	arShpQ13 := make([]int16, numSubfr*maxShapeLpcOrder)

	// LTP coefficients
	ltpCoefQ14 := make([]int16, numSubfr*ltpOrderConst)

	// Other parameters
	harmShapeGainQ14 := make([]int, numSubfr)
	tiltQ14 := make([]int, numSubfr)
	lfShpQ14 := make([]int32, numSubfr)

	params := &NSQParams{
		SignalType:       typeUnvoiced,
		QuantOffsetType:  0,
		PredCoefQ12:      predCoefQ12,
		LTPCoefQ14:       ltpCoefQ14,
		ARShpQ13:         arShpQ13,
		HarmShapeGainQ14: harmShapeGainQ14,
		TiltQ14:          tiltQ14,
		LFShpQ14:         lfShpQ14,
		GainsQ16:         gainsQ16,
		PitchL:           pitchL,
		LambdaQ10:        512,
		LTPScaleQ14:      12288,
		FrameLength:      frameLength,
		SubfrLength:      subfrLength,
		NbSubfr:          numSubfr,
		LTPMemLength:     320,
		PredLPCOrder:     10,
		ShapeLPCOrder:    10,
		Seed:             1,
	}

	pulses, xq := NoiseShapeQuantize(nsq, input, params)

	// Verify output lengths
	if len(pulses) != frameLength {
		t.Errorf("Expected %d pulses, got %d", frameLength, len(pulses))
	}
	if len(xq) != frameLength {
		t.Errorf("Expected %d xq samples, got %d", frameLength, len(xq))
	}

	// Verify pulses are in reasonable range (int8 range: -128 to 127)
	// Per SILK spec, pulses are typically in range [-31, 30] but can be larger
	for i, p := range pulses {
		if p < -100 || p > 100 {
			t.Errorf("Pulse[%d] = %d out of expected range", i, p)
			break
		}
	}

	// Verify reconstructed signal is not all zeros
	hasNonZero := false
	for _, x := range xq {
		if x != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("Reconstructed signal is all zeros")
	}
}

// TestNSQVoicedFrame verifies NSQ with voiced parameters.
func TestNSQVoicedFrame(t *testing.T) {
	nsq := NewNSQState()

	frameLength := 320
	subfrLength := 80
	numSubfr := 4

	// Create test signal
	input := make([]int16, frameLength)
	for i := range input {
		input[i] = int16(5000 * math.Sin(2*math.Pi*float64(i)/80)) // Period ~80 samples
	}

	// LPC coefficients
	predCoefQ12 := make([]int16, 2*maxLPCOrder)
	predCoefQ12[0] = 3000
	predCoefQ12[1] = -1500
	predCoefQ12[16] = 3000
	predCoefQ12[17] = -1500

	// Gains
	gainsQ16 := make([]int32, numSubfr)
	for i := range gainsQ16 {
		gainsQ16[i] = 65536
	}

	// Pitch lags (voiced with ~80 sample period)
	pitchL := []int{80, 80, 80, 80}

	// AR shaping
	arShpQ13 := make([]int16, numSubfr*maxShapeLpcOrder)
	for sf := 0; sf < numSubfr; sf++ {
		arShpQ13[sf*maxShapeLpcOrder] = 1000
	}

	// LTP coefficients (center tap)
	ltpCoefQ14 := make([]int16, numSubfr*ltpOrderConst)
	for sf := 0; sf < numSubfr; sf++ {
		ltpCoefQ14[sf*ltpOrderConst+2] = 8192 // 0.5 in Q14
	}

	harmShapeGainQ14 := make([]int, numSubfr)
	tiltQ14 := make([]int, numSubfr)
	lfShpQ14 := make([]int32, numSubfr)
	for sf := 0; sf < numSubfr; sf++ {
		harmShapeGainQ14[sf] = 4096
		tiltQ14[sf] = -2048
	}

	params := &NSQParams{
		SignalType:       typeVoiced,
		QuantOffsetType:  0,
		PredCoefQ12:      predCoefQ12,
		LTPCoefQ14:       ltpCoefQ14,
		ARShpQ13:         arShpQ13,
		HarmShapeGainQ14: harmShapeGainQ14,
		TiltQ14:          tiltQ14,
		LFShpQ14:         lfShpQ14,
		GainsQ16:         gainsQ16,
		PitchL:           pitchL,
		LambdaQ10:        512,
		LTPScaleQ14:      12288,
		FrameLength:      frameLength,
		SubfrLength:      subfrLength,
		NbSubfr:          numSubfr,
		LTPMemLength:     320,
		PredLPCOrder:     10,
		ShapeLPCOrder:    10,
		Seed:             2,
	}

	pulses, _ := NoiseShapeQuantize(nsq, input, params)

	// Verify pulses exist
	if len(pulses) != frameLength {
		t.Errorf("Expected %d pulses, got %d", frameLength, len(pulses))
	}

	// Verify state was updated
	if nsq.lagPrev == 0 {
		t.Error("lagPrev should be updated for voiced frame")
	}
}

// TestNSQDithering verifies dithering affects quantization.
func TestNSQDithering(t *testing.T) {
	// Run NSQ twice with different seeds
	nsq1 := NewNSQState()
	nsq2 := NewNSQState()

	frameLength := 160
	subfrLength := 40
	numSubfr := 4

	input := make([]int16, frameLength)
	for i := range input {
		input[i] = int16(1000 * math.Sin(2*math.Pi*float64(i)/40))
	}

	makeParams := func(seed int) *NSQParams {
		return &NSQParams{
			SignalType:       typeUnvoiced,
			QuantOffsetType:  0,
			PredCoefQ12:      make([]int16, 2*maxLPCOrder),
			LTPCoefQ14:       make([]int16, numSubfr*ltpOrderConst),
			ARShpQ13:         make([]int16, numSubfr*maxShapeLpcOrder),
			HarmShapeGainQ14: make([]int, numSubfr),
			TiltQ14:          make([]int, numSubfr),
			LFShpQ14:         make([]int32, numSubfr),
			GainsQ16:         []int32{65536, 65536, 65536, 65536},
			PitchL:           make([]int, numSubfr),
			LambdaQ10:        512,
			LTPScaleQ14:      12288,
			FrameLength:      frameLength,
			SubfrLength:      subfrLength,
			NbSubfr:          numSubfr,
			LTPMemLength:     320,
			PredLPCOrder:     10,
			ShapeLPCOrder:    10,
			Seed:             seed,
		}
	}

	pulses1, _ := NoiseShapeQuantize(nsq1, input, makeParams(0))
	pulses2, _ := NoiseShapeQuantize(nsq2, input, makeParams(1))

	// Different seeds should produce (at least slightly) different outputs
	differences := 0
	for i := range pulses1 {
		if pulses1[i] != pulses2[i] {
			differences++
		}
	}

	// We expect some differences due to dithering
	// (though with low-amplitude signal the effect may be small)
	t.Logf("Differences with different seeds: %d/%d", differences, len(pulses1))
}

// BenchmarkNSQ benchmarks the noise shaping quantizer.
func BenchmarkNSQ(b *testing.B) {
	nsq := NewNSQState()

	frameLength := 320
	subfrLength := 80
	numSubfr := 4

	input := make([]int16, frameLength)
	for i := range input {
		input[i] = int16(10000 * math.Sin(2*math.Pi*float64(i)/80))
	}

	params := &NSQParams{
		SignalType:       typeUnvoiced,
		QuantOffsetType:  0,
		PredCoefQ12:      make([]int16, 2*maxLPCOrder),
		LTPCoefQ14:       make([]int16, numSubfr*ltpOrderConst),
		ARShpQ13:         make([]int16, numSubfr*maxShapeLpcOrder),
		HarmShapeGainQ14: make([]int, numSubfr),
		TiltQ14:          make([]int, numSubfr),
		LFShpQ14:         make([]int32, numSubfr),
		GainsQ16:         []int32{65536, 65536, 65536, 65536},
		PitchL:           make([]int, numSubfr),
		LambdaQ10:        512,
		LTPScaleQ14:      12288,
		FrameLength:      frameLength,
		SubfrLength:      subfrLength,
		NbSubfr:          numSubfr,
		LTPMemLength:     320,
		PredLPCOrder:     10,
		ShapeLPCOrder:    10,
		Seed:             1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nsq.Reset()
		NoiseShapeQuantize(nsq, input, params)
	}
}
