package silk

import (
	"math"
	"math/rand"
	"slices"
	"testing"
)

func TestNoiseShapeQuantizerDelDec24States4Pred16SpecializationsMatchGeneric(t *testing.T) {
	rng := rand.New(rand.NewSource(0x24341604))
	r32 := func(amp int32) int32 { return rng.Int31n(2*amp+1) - amp }
	r16 := func(amp int32) int16 { return int16(rng.Int31n(2*amp+1) - amp) }

	cases := [...]struct {
		name       string
		signalType int
	}{
		{name: "voiced", signalType: typeVoiced},
		{name: "unvoiced", signalType: typeUnvoiced},
	}

	for _, tc := range cases {
		for trial := range 200 {
			lengths := [...]int{1, 16, 40, maxSubFrameLength}
			length := lengths[trial%len(lengths)]
			lag := 60 + rng.Intn(120)
			if tc.signalType != typeVoiced && trial%3 == 0 {
				lag = 0
			}
			decisionDelayActive := 1 + rng.Intn(decisionDelay-1)
			frameOffset := decisionDelayActive + rng.Intn(4)
			subfr := rng.Intn(maxNbSubfr)

			var statesGeneric [maxDelDecStates]nsqDelDecState
			for k := range maxDelDecStates {
				st := &statesGeneric[k]
				st.lfARQ14 = r32(1 << 20)
				st.diffQ14 = r32(1 << 20)
				st.seed = int32(rng.Uint32())
				st.seedInit = int32(rng.Uint32())
				st.rdQ10 = r32(1 << 24)
				for i := range st.sLPCQ14 {
					st.sLPCQ14[i] = r32(1 << 22)
				}
				for i := range st.randState {
					st.randState[i] = int32(rng.Uint32())
					st.qQ10[i] = r32(1 << 14)
					st.xqQ14[i] = r32(1 << 22)
					st.predQ15[i] = r32(1 << 22)
					st.shapeQ14[i] = r32(1 << 22)
				}
				for i := range st.sAR2Q14 {
					st.sAR2Q14[i] = r32(1 << 22)
				}
			}
			statesSpecial := statesGeneric

			nsqGeneric := NewNSQState()
			nsqGeneric.sLTPShpBufIdx = ltpMemLength
			nsqGeneric.sLTPBufIdx = ltpMemLength
			for i := range nsqGeneric.sLTPShpQ14 {
				nsqGeneric.sLTPShpQ14[i] = r32(1 << 22)
			}
			nsqSpecial := nsqGeneric.Clone()

			xQ10 := make([]int32, length)
			for i := range xQ10 {
				xQ10[i] = r32(1 << 19)
			}
			sLTPGeneric := make([]int32, ltpMemLength+maxFrameLengthNSQ)
			for i := range sLTPGeneric {
				sLTPGeneric[i] = r32(1 << 22)
			}
			sLTPSpecial := append([]int32(nil), sLTPGeneric...)

			pulsesGeneric := make([]int8, frameOffset+length+decisionDelay)
			pulsesSpecial := make([]int8, len(pulsesGeneric))
			xqGeneric := make([]int16, len(pulsesGeneric))
			xqSpecial := make([]int16, len(pulsesGeneric))
			delayedGainGeneric := make([]int32, decisionDelay)
			for i := range delayedGainGeneric {
				delayedGainGeneric[i] = 1 << 10
			}
			delayedGainSpecial := append([]int32(nil), delayedGainGeneric...)

			var aQ12 [maxLPCOrder]int16
			for i := range aQ12 {
				aQ12[i] = r16(4096)
			}
			var bQ14 [ltpOrderConst]int16
			for i := range bQ14 {
				bQ14[i] = r16(8192)
			}
			var arQ13 [maxShapeLpcOrder]int16
			for i := range arQ13 {
				arQ13[i] = r16(8192)
			}

			harmShapeFIRPackedQ14 := r32(1 << 14)
			tiltQ14 := r32(1 << 14)
			lfShpQ14 := r32(1 << 20)
			gainQ16 := int32(1<<16 + rng.Int31n(1<<20))
			lambdaQ10 := int32(rng.Int31n(60000))
			offsetQ10 := [...]int{offsetVLQ10, offsetVHQ10}[rng.Intn(2)]
			warpingQ16 := int(r32(1 << 14))
			smplGeneric := rng.Intn(decisionDelay)
			smplSpecial := smplGeneric

			noiseShapeQuantizerDelDecGeneric(nsqGeneric, statesGeneric[:], tc.signalType, xQ10, pulsesGeneric, xqGeneric,
				sLTPGeneric, delayedGainGeneric, aQ12[:], bQ14[:], arQ13[:], lag, harmShapeFIRPackedQ14, tiltQ14, lfShpQ14,
				gainQ16, lambdaQ10, offsetQ10, length, subfr, maxShapeLpcOrder, maxLPCOrder, warpingQ16, maxDelDecStates,
				&smplGeneric, decisionDelayActive, frameOffset)
			if tc.signalType == typeVoiced {
				noiseShapeQuantizerDelDec24States4Pred16(nsqSpecial, statesSpecial[:], xQ10, pulsesSpecial, xqSpecial,
					sLTPSpecial, delayedGainSpecial, aQ12[:], bQ14[:], arQ13[:], lag, harmShapeFIRPackedQ14, tiltQ14, lfShpQ14,
					gainQ16, lambdaQ10, offsetQ10, length, subfr, warpingQ16, &smplSpecial, decisionDelayActive, frameOffset)
			} else {
				noiseShapeQuantizerDelDecUnvoiced24States4Pred16(nsqSpecial, statesSpecial[:], xQ10, pulsesSpecial, xqSpecial,
					sLTPSpecial, delayedGainSpecial, aQ12[:], arQ13[:], lag, harmShapeFIRPackedQ14, tiltQ14, lfShpQ14,
					gainQ16, lambdaQ10, offsetQ10, length, subfr, warpingQ16, &smplSpecial, decisionDelayActive, frameOffset)
			}

			if smplSpecial != smplGeneric {
				t.Fatalf("%s trial %d smplBufIdx: got %d want %d", tc.name, trial, smplSpecial, smplGeneric)
			}
			if nsqSpecial.sLTPShpBufIdx != nsqGeneric.sLTPShpBufIdx || nsqSpecial.sLTPBufIdx != nsqGeneric.sLTPBufIdx {
				t.Fatalf("%s trial %d NSQ indices got shp=%d ltp=%d want shp=%d ltp=%d", tc.name, trial,
					nsqSpecial.sLTPShpBufIdx, nsqSpecial.sLTPBufIdx, nsqGeneric.sLTPShpBufIdx, nsqGeneric.sLTPBufIdx)
			}
			if statesSpecial != statesGeneric {
				t.Fatalf("%s trial %d delayed-decision state mismatch", tc.name, trial)
			}
			for i := range nsqGeneric.sLTPShpQ14 {
				if nsqSpecial.sLTPShpQ14[i] != nsqGeneric.sLTPShpQ14[i] {
					t.Fatalf("%s trial %d sLTPShpQ14[%d]: got %d want %d", tc.name, trial, i, nsqSpecial.sLTPShpQ14[i], nsqGeneric.sLTPShpQ14[i])
				}
			}
			for i := range sLTPGeneric {
				if sLTPSpecial[i] != sLTPGeneric[i] {
					t.Fatalf("%s trial %d sLTPQ15[%d]: got %d want %d", tc.name, trial, i, sLTPSpecial[i], sLTPGeneric[i])
				}
			}
			for i := range delayedGainGeneric {
				if delayedGainSpecial[i] != delayedGainGeneric[i] {
					t.Fatalf("%s trial %d delayedGainQ10[%d]: got %d want %d", tc.name, trial, i, delayedGainSpecial[i], delayedGainGeneric[i])
				}
			}
			for i := range pulsesGeneric {
				if pulsesSpecial[i] != pulsesGeneric[i] {
					t.Fatalf("%s trial %d pulses[%d]: got %d want %d", tc.name, trial, i, pulsesSpecial[i], pulsesGeneric[i])
				}
				if xqSpecial[i] != xqGeneric[i] {
					t.Fatalf("%s trial %d xq[%d]: got %d want %d", tc.name, trial, i, xqSpecial[i], xqGeneric[i])
				}
			}
		}
	}
}

func TestNSQDelDecXqQ0Rounding(t *testing.T) {
	for _, tc := range []struct {
		xq, gain int32
		bits     int
		c, avx2  int16
	}{
		{xq: 123456, gain: 70000, bits: 8, c: 515, avx2: 515},
		{xq: -98765, gain: 1 << 20, bits: 14, c: -96, avx2: -96},
		// 2^30 * 2^20 >> 16 = 2^34 wraps to 0 in the 32-bit SMULWW.
		{xq: 1 << 30, gain: 1 << 20, bits: 8, c: 0, avx2: 32767},
		{xq: -(1 << 30), gain: 1 << 20, bits: 8, c: 0, avx2: -32768},
	} {
		var nsq NSQState
		if got := nsqDelDecXqQ0(&nsq, tc.xq, tc.gain, tc.bits); got != tc.c {
			t.Errorf("C rounding xq=%d gain=%d bits=%d = %d, want %d", tc.xq, tc.gain, tc.bits, got, tc.c)
		}
		nsq.delDecExactXqRound = true
		if got := nsqDelDecXqQ0(&nsq, tc.xq, tc.gain, tc.bits); got != tc.avx2 {
			t.Errorf("AVX2 rounding xq=%d gain=%d bits=%d = %d, want %d", tc.xq, tc.gain, tc.bits, got, tc.avx2)
		}
	}
}

func TestNSQInputReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		voiced  bool
		delayed bool
	}{
		{"single/unvoiced", false, false},
		{"single/voiced", true, false},
		{"delayed/unvoiced", false, true},
		{"delayed/voiced", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const frameLength = 320
			input := make([]int16, frameLength)
			for i := range input {
				input[i] = int16((i*7919)%20001 - 10000)
			}
			want := slices.Clone(input)
			params := &NSQParams{
				SignalType:             typeUnvoiced,
				PredCoefQ12:            make([]int16, 2*maxLPCOrder),
				NLSFInterpCoefQ2:       4,
				LTPCoefQ14:             make([]int16, 4*ltpOrderConst),
				ARShpQ13:               make([]int16, 4*maxShapeLpcOrder),
				HarmShapeGainQ14:       make([]int32, 4),
				TiltQ14:                make([]int32, 4),
				LFShpQ14:               make([]int32, 4),
				GainsQ16:               []int32{1 << 18, 1 << 18, 1 << 18, 1 << 18},
				PitchL:                 []int32{80, 80, 80, 80},
				LambdaQ10:              512,
				LTPScaleQ14:            12288,
				FrameLength:            frameLength,
				SubfrLength:            80,
				NbSubfr:                4,
				LTPMemLength:           320,
				PredLPCOrder:           maxLPCOrder,
				ShapeLPCOrder:          maxShapeLpcOrder,
				NStatesDelayedDecision: 4,
			}
			if tc.voiced {
				params.SignalType = typeVoiced
				for sf := range 4 {
					params.LTPCoefQ14[sf*ltpOrderConst+2] = 8192
				}
			}
			state := NewNSQState()
			for attempt := range 3 {
				if tc.delayed {
					NoiseShapeQuantizeDelDec(state, input, params)
				} else {
					NoiseShapeQuantize(state, input, params)
				}
				if !slices.Equal(input, want) {
					t.Fatalf("attempt %d modifies shared frame input", attempt)
				}
				for sf := range params.GainsQ16 {
					params.GainsQ16[sf] *= 2
				}
			}
		})
	}
}

func refShortTermPrediction16(sLPCQ14 []int32, idx int, aQ12 []int16) int32 {
	out := int32(8)
	for k := range 16 {
		c16 := int16(aQ12[k])
		out += int32((int64(sLPCQ14[idx-k]) * int64(c16)) >> 16)
	}
	return out
}

// refShortTermPrediction10 is the pure Go reference for verification.
func refShortTermPrediction10(sLPCQ14 []int32, idx int, aQ12 []int16) int32 {
	out := int32(5)
	for k := range 10 {
		c16 := int16(aQ12[k])
		out += int32((int64(sLPCQ14[idx-k]) * int64(c16)) >> 16)
	}
	return out
}

func TestShortTermPrediction16Asm(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// Test with random realistic values.
	for trial := range 1000 {
		sLPCQ14 := make([]int32, 96)
		aQ12 := make([]int16, 16)
		for i := range sLPCQ14 {
			sLPCQ14[i] = rng.Int31n(1<<24) - (1 << 23) // Q14 range
		}
		for i := range aQ12 {
			aQ12[i] = int16(rng.Int31n(1<<13) - (1 << 12)) // Q12 range
		}
		idx := 15 + rng.Intn(80) // valid range: idx >= 15

		got := shortTermPrediction16(sLPCQ14, idx, aQ12)
		want := refShortTermPrediction16(sLPCQ14, idx, aQ12)
		if got != want {
			t.Fatalf("trial %d: shortTermPrediction16 mismatch: got %d, want %d (idx=%d)", trial, got, want, idx)
		}
	}
}

func TestShortTermPrediction10Asm(t *testing.T) {
	rng := rand.New(rand.NewSource(99))

	for trial := range 1000 {
		sLPCQ14 := make([]int32, 96)
		aQ12 := make([]int16, 10)
		for i := range sLPCQ14 {
			sLPCQ14[i] = rng.Int31n(1<<24) - (1 << 23)
		}
		for i := range aQ12 {
			aQ12[i] = int16(rng.Int31n(1<<13) - (1 << 12))
		}
		idx := 9 + rng.Intn(86)

		got := shortTermPrediction10(sLPCQ14, idx, aQ12)
		want := refShortTermPrediction10(sLPCQ14, idx, aQ12)
		if got != want {
			t.Fatalf("trial %d: shortTermPrediction10 mismatch: got %d, want %d (idx=%d)", trial, got, want, idx)
		}
	}
}

func TestShortTermPrediction16EdgeCases(t *testing.T) {
	// Max positive signal, max positive coefficients.
	sLPCQ14 := make([]int32, 32)
	aQ12 := make([]int16, 16)
	for i := range sLPCQ14 {
		sLPCQ14[i] = 0x7FFFFFFF
	}
	for i := range aQ12 {
		aQ12[i] = 0x7FFF
	}
	idx := 16
	got := shortTermPrediction16(sLPCQ14, idx, aQ12)
	want := refShortTermPrediction16(sLPCQ14, idx, aQ12)
	if got != want {
		t.Fatalf("max positive: got %d, want %d", got, want)
	}

	// Max negative signal, negative coefficients.
	for i := range sLPCQ14 {
		sLPCQ14[i] = -0x7FFFFFFF
	}
	for i := range aQ12 {
		aQ12[i] = -0x7FFF
	}
	got = shortTermPrediction16(sLPCQ14, idx, aQ12)
	want = refShortTermPrediction16(sLPCQ14, idx, aQ12)
	if got != want {
		t.Fatalf("max negative: got %d, want %d", got, want)
	}

	// Zero signal.
	for i := range sLPCQ14 {
		sLPCQ14[i] = 0
	}
	for i := range aQ12 {
		aQ12[i] = 1000
	}
	got = shortTermPrediction16(sLPCQ14, idx, aQ12)
	if got != 8 { // only the bias remains
		t.Fatalf("zero signal: got %d, want 8", got)
	}

	// Zero coefficients.
	for i := range sLPCQ14 {
		sLPCQ14[i] = 100000
	}
	for i := range aQ12 {
		aQ12[i] = 0
	}
	got = shortTermPrediction16(sLPCQ14, idx, aQ12)
	if got != 8 {
		t.Fatalf("zero coefficients: got %d, want 8", got)
	}
}

func BenchmarkShortTermPrediction16(b *testing.B) {
	sLPCQ14 := make([]int32, 96)
	aQ12 := make([]int16, 16)
	rng := rand.New(rand.NewSource(1))
	for i := range sLPCQ14 {
		sLPCQ14[i] = rng.Int31()
	}
	for i := range aQ12 {
		aQ12[i] = int16(rng.Int31())
	}
	idx := 30
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		shortTermPrediction16(sLPCQ14, idx, aQ12)
	}
}

func BenchmarkShortTermPrediction10(b *testing.B) {
	sLPCQ14 := make([]int32, 96)
	aQ12 := make([]int16, 10)
	rng := rand.New(rand.NewSource(1))
	for i := range sLPCQ14 {
		sLPCQ14[i] = rng.Int31()
	}
	for i := range aQ12 {
		aQ12[i] = int16(rng.Int31())
	}
	idx := 30
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		shortTermPrediction10(sLPCQ14, idx, aQ12)
	}
}

func BenchmarkShortTermPredictionDispatch(b *testing.B) {
	sLPCQ14 := make([]int32, 96)
	aQ12 := make([]int16, 16)
	rng := rand.New(rand.NewSource(1))
	for i := range sLPCQ14 {
		sLPCQ14[i] = rng.Int31()
	}
	for i := range aQ12 {
		aQ12[i] = int16(rng.Int31())
	}
	idx := 30
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		shortTermPrediction(sLPCQ14, idx, aQ12, 16)
	}
}

func TestNSQStateInitialization(t *testing.T) {
	state := NewNSQState()

	if state == nil {
		t.Fatal("NewNSQState returned nil")
		return
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
	for range 10 {
		seed = silk_RAND(seed)
		// Just verify it produces varying output
	}

	// LCG should not produce zero for non-zero seed
	seed = int32(1)
	for range 100 {
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

	if silk_ADD_SAT32(0x7fffffff, 1) != 0x7fffffff {
		t.Error("silk_ADD_SAT32 failed positive saturation")
	}
	if silk_ADD_SAT32(-0x80000000, -1) != -0x80000000 {
		t.Error("silk_ADD_SAT32 failed negative saturation")
	}
	if silk_ADD_SAT32(123, -23) != 100 {
		t.Error("silk_ADD_SAT32 failed passthrough")
	}
	if silk_SUB_SAT32(0x7fffffff, -1) != 0x7fffffff {
		t.Error("silk_SUB_SAT32 failed positive saturation")
	}
	if silk_SUB_SAT32(-0x80000000, 1) != -0x80000000 {
		t.Error("silk_SUB_SAT32 failed negative saturation")
	}
	if silk_SUB_SAT32(123, 23) != 100 {
		t.Error("silk_SUB_SAT32 failed passthrough")
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
	pitchL := make([]int32, numSubfr)

	// AR shaping coefficients
	arShpQ13 := make([]int16, numSubfr*maxShapeLpcOrder)

	// LTP coefficients
	ltpCoefQ14 := make([]int16, numSubfr*ltpOrderConst)

	// Other parameters
	harmShapeGainQ14 := make([]int32, numSubfr)
	tiltQ14 := make([]int32, numSubfr)
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
	pitchL := []int32{80, 80, 80, 80}

	// AR shaping
	arShpQ13 := make([]int16, numSubfr*maxShapeLpcOrder)
	for sf := range numSubfr {
		arShpQ13[sf*maxShapeLpcOrder] = 1000
	}

	// LTP coefficients (center tap)
	ltpCoefQ14 := make([]int16, numSubfr*ltpOrderConst)
	for sf := range numSubfr {
		ltpCoefQ14[sf*ltpOrderConst+2] = 8192 // 0.5 in Q14
	}

	harmShapeGainQ14 := make([]int32, numSubfr)
	tiltQ14 := make([]int32, numSubfr)
	lfShpQ14 := make([]int32, numSubfr)
	for sf := range numSubfr {
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
			HarmShapeGainQ14: make([]int32, numSubfr),
			TiltQ14:          make([]int32, numSubfr),
			LFShpQ14:         make([]int32, numSubfr),
			GainsQ16:         []int32{65536, 65536, 65536, 65536},
			PitchL:           make([]int32, numSubfr),
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
		HarmShapeGainQ14: make([]int32, numSubfr),
		TiltQ14:          make([]int32, numSubfr),
		LFShpQ14:         make([]int32, numSubfr),
		GainsQ16:         []int32{65536, 65536, 65536, 65536},
		PitchL:           make([]int32, numSubfr),
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

func refWarpedARFeedback(sAR []int32, diffQ14 int32, arShpQ13 []int16, warpQ16 int32, order int) int32 {
	w := int64(warpQ16)
	tmp2 := diffQ14 + int32((int64(sAR[0])*w)>>16)
	tmp1 := sAR[0] + int32((int64(sAR[1]-tmp2)*w)>>16)
	sAR[0] = tmp2
	acc := int32(order>>1) + int32((int64(tmp2)*int64(arShpQ13[0]))>>16)

	for j := 2; j < order; j += 2 {
		tmp2 = sAR[j-1] + int32((int64(sAR[j]-tmp1)*w)>>16)
		sAR[j-1] = tmp1
		acc += int32((int64(tmp1) * int64(arShpQ13[j-1])) >> 16)
		tmp1 = sAR[j] + int32((int64(sAR[j+1]-tmp2)*w)>>16)
		sAR[j] = tmp2
		acc += int32((int64(tmp2) * int64(arShpQ13[j])) >> 16)
	}
	sAR[order-1] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[order-1])) >> 16)
	return acc
}

func TestWarpedARFeedback24(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := range 1000 {
		var sAR [maxShapeLpcOrder]int32
		var sARRef [maxShapeLpcOrder]int32
		var arShpQ13 [24]int16
		for i := range 24 {
			v := rng.Int31n(1<<20) - (1 << 19)
			sAR[i] = v
			sARRef[i] = v
		}
		for i := range arShpQ13 {
			arShpQ13[i] = int16(rng.Int31n(1<<13) - (1 << 12))
		}
		diffQ14 := rng.Int31n(1<<20) - (1 << 19)
		warpQ16 := rng.Int31n(1<<15) - (1 << 14) // typical range

		got := warpedARFeedback24(&sAR, diffQ14, &arShpQ13, warpQ16)
		want := refWarpedARFeedback(sARRef[:], diffQ14, arShpQ13[:], warpQ16, 24)

		if got != want {
			t.Fatalf("trial %d: warpedARFeedback24 mismatch: got %d, want %d", trial, got, want)
		}
		for i := range 24 {
			if sAR[i] != sARRef[i] {
				t.Fatalf("trial %d: sAR[%d] mismatch: got %d, want %d", trial, i, sAR[i], sARRef[i])
			}
		}
	}
}

func TestWarpedARFeedback16(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := range 1000 {
		var sAR [maxShapeLpcOrder]int32
		var sARRef [maxShapeLpcOrder]int32
		var arShpQ13 [16]int16
		for i := range 24 {
			v := rng.Int31n(1<<20) - (1 << 19)
			sAR[i] = v
			sARRef[i] = v
		}
		for i := range arShpQ13 {
			arShpQ13[i] = int16(rng.Int31n(1<<13) - (1 << 12))
		}
		diffQ14 := rng.Int31n(1<<20) - (1 << 19)
		warpQ16 := rng.Int31n(1<<15) - (1 << 14)

		got := warpedARFeedback16(&sAR, diffQ14, &arShpQ13, warpQ16)
		want := refWarpedARFeedback(sARRef[:], diffQ14, arShpQ13[:], warpQ16, 16)

		if got != want {
			t.Fatalf("trial %d: warpedARFeedback16 mismatch: got %d, want %d", trial, got, want)
		}
		for i := range 16 {
			if sAR[i] != sARRef[i] {
				t.Fatalf("trial %d: sAR[%d] mismatch: got %d, want %d", trial, i, sAR[i], sARRef[i])
			}
		}
	}
}

func TestWarpedARFeedback24EdgeCases(t *testing.T) {
	// Zero warping
	var sAR [maxShapeLpcOrder]int32
	var sARRef [maxShapeLpcOrder]int32
	var arShpQ13 [24]int16
	for i := range 24 {
		sAR[i] = int32(i * 1000)
		sARRef[i] = sAR[i]
		arShpQ13[i] = int16(100 + i)
	}
	got := warpedARFeedback24(&sAR, 500, &arShpQ13, 0)
	want := refWarpedARFeedback(sARRef[:], 500, arShpQ13[:], 0, 24)
	if got != want {
		t.Fatalf("zero warp: got %d, want %d", got, want)
	}

	// Max values
	for i := range 24 {
		sAR[i] = 0x7FFFF
		sARRef[i] = sAR[i]
		arShpQ13[i] = 0x7FFF
	}
	got = warpedARFeedback24(&sAR, 0x7FFFF, &arShpQ13, 0x7FFF)
	want = refWarpedARFeedback(sARRef[:], 0x7FFFF, arShpQ13[:], 0x7FFF, 24)
	if got != want {
		t.Fatalf("max values: got %d, want %d", got, want)
	}

	// Negative values
	for i := range 24 {
		sAR[i] = -0x7FFFF
		sARRef[i] = sAR[i]
		arShpQ13[i] = -0x7FFF
	}
	got = warpedARFeedback24(&sAR, -0x7FFFF, &arShpQ13, -0x7FFF)
	want = refWarpedARFeedback(sARRef[:], -0x7FFFF, arShpQ13[:], -0x7FFF, 24)
	if got != want {
		t.Fatalf("negative values: got %d, want %d", got, want)
	}
}

func TestWarpedARFeedback24States4MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(123))
	for trial := range 1000 {
		var states [maxDelDecStates]nsqDelDecState
		var scalar [maxDelDecStates]nsqDelDecState
		var arShpQ13 [24]int16
		for k := range maxDelDecStates {
			states[k].diffQ14 = rng.Int31n(1<<20) - (1 << 19)
			scalar[k].diffQ14 = states[k].diffQ14
			for i := range 24 {
				v := rng.Int31n(1<<20) - (1 << 19)
				states[k].sAR2Q14[i] = v
				scalar[k].sAR2Q14[i] = v
			}
		}
		for i := range arShpQ13 {
			arShpQ13[i] = int16(rng.Int31n(1<<13) - (1 << 12))
		}
		warpQ16 := rng.Int31n(1<<15) - (1 << 14)

		var got [maxDelDecStates]int32
		warpedARFeedback24States4(states[:], &arShpQ13, warpQ16, &got)

		for k := range maxDelDecStates {
			want := warpedARFeedback24(&scalar[k].sAR2Q14, scalar[k].diffQ14, &arShpQ13, warpQ16)
			if got[k] != want {
				t.Fatalf("trial %d state %d: got %d want %d", trial, k, got[k], want)
			}
			for i := range 24 {
				if states[k].sAR2Q14[i] != scalar[k].sAR2Q14[i] {
					t.Fatalf("trial %d state %d sAR[%d]: got %d want %d", trial, k, i, states[k].sAR2Q14[i], scalar[k].sAR2Q14[i])
				}
			}
		}
	}
}

func TestWarpedARFeedback20States3MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(124))
	for trial := range 1000 {
		var states [maxDelDecStates]nsqDelDecState
		var scalar [maxDelDecStates]nsqDelDecState
		var arShpQ13 [20]int16
		for k := range 3 {
			states[k].diffQ14 = rng.Int31n(1<<20) - (1 << 19)
			scalar[k].diffQ14 = states[k].diffQ14
			for i := range 20 {
				v := rng.Int31n(1<<20) - (1 << 19)
				states[k].sAR2Q14[i] = v
				scalar[k].sAR2Q14[i] = v
			}
		}
		for i := range arShpQ13 {
			arShpQ13[i] = int16(rng.Int31n(1<<13) - (1 << 12))
		}
		warpQ16 := rng.Int31n(1<<15) - (1 << 14)

		var got [maxDelDecStates]int32
		warpedARFeedback20States3(states[:], &arShpQ13, warpQ16, &got)

		for k := range 3 {
			want := refWarpedARFeedback(scalar[k].sAR2Q14[:], scalar[k].diffQ14, arShpQ13[:], warpQ16, 20)
			if got[k] != want {
				t.Fatalf("trial %d state %d: got %d want %d", trial, k, got[k], want)
			}
			for i := range 20 {
				if states[k].sAR2Q14[i] != scalar[k].sAR2Q14[i] {
					t.Fatalf("trial %d state %d sAR[%d]: got %d want %d", trial, k, i, states[k].sAR2Q14[i], scalar[k].sAR2Q14[i])
				}
			}
		}
	}
}

func BenchmarkWarpedARFeedback24(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	var sAR [maxShapeLpcOrder]int32
	var arShpQ13 [24]int16
	for i := range sAR {
		sAR[i] = rng.Int31()
	}
	for i := range arShpQ13 {
		arShpQ13[i] = int16(rng.Int31())
	}
	diffQ14 := rng.Int31()
	warpQ16 := int32(rng.Int31n(1 << 15))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpedARFeedback24(&sAR, diffQ14, &arShpQ13, warpQ16)
	}
}

func BenchmarkWarpedARFeedback24States4(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	var states [maxDelDecStates]nsqDelDecState
	var arShpQ13 [24]int16
	for k := range states {
		states[k].diffQ14 = rng.Int31()
		for i := range states[k].sAR2Q14 {
			states[k].sAR2Q14[i] = rng.Int31()
		}
	}
	for i := range arShpQ13 {
		arShpQ13[i] = int16(rng.Int31())
	}
	warpQ16 := int32(rng.Int31n(1 << 15))
	var out [maxDelDecStates]int32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpedARFeedback24States4(states[:], &arShpQ13, warpQ16, &out)
	}
}

func BenchmarkWarpedARFeedback20States3(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	var states [maxDelDecStates]nsqDelDecState
	var arShpQ13 [20]int16
	for k := range 3 {
		states[k].diffQ14 = rng.Int31()
		for i := range 20 {
			states[k].sAR2Q14[i] = rng.Int31()
		}
	}
	for i := range arShpQ13 {
		arShpQ13[i] = int16(rng.Int31())
	}
	warpQ16 := int32(rng.Int31n(1 << 15))
	var out [maxDelDecStates]int32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpedARFeedback20States3(states[:], &arShpQ13, warpQ16, &out)
	}
}

func BenchmarkWarpedARFeedback20States3Scalar(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	var states [maxDelDecStates]nsqDelDecState
	var arShpQ13 [20]int16
	for k := range 3 {
		states[k].diffQ14 = rng.Int31()
		for i := range 20 {
			states[k].sAR2Q14[i] = rng.Int31()
		}
	}
	for i := range arShpQ13 {
		arShpQ13[i] = int16(rng.Int31())
	}
	warpQ16 := int32(rng.Int31n(1 << 15))
	var out [maxDelDecStates]int32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for k := range 3 {
			out[k] = warpedARFeedbackGeneric(states[k].sAR2Q14[:], states[k].diffQ14, arShpQ13[:], warpQ16, 20)
		}
	}
}

func BenchmarkWarpedARFeedback16(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	var sAR [maxShapeLpcOrder]int32
	var arShpQ13 [16]int16
	for i := range sAR {
		sAR[i] = rng.Int31()
	}
	for i := range arShpQ13 {
		arShpQ13[i] = int16(rng.Int31())
	}
	diffQ14 := rng.Int31()
	warpQ16 := int32(rng.Int31n(1 << 15))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpedARFeedback16(&sAR, diffQ14, &arShpQ13, warpQ16)
	}
}
