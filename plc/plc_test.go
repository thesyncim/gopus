package plc

import (
	"math"
	"testing"
)

// TestPLCState tests basic PLC state tracking.
func TestPLCState(t *testing.T) {
	state := NewState()

	// Initial state
	if state.LostCount() != 0 {
		t.Errorf("initial lostCount = %d, want 0", state.LostCount())
	}
	if state.FadeFactor() != 1.0 {
		t.Errorf("initial fadeFactor = %f, want 1.0", state.FadeFactor())
	}
	if state.IsExhausted() {
		t.Error("initial state should not be exhausted")
	}

	// Record first loss
	fade1 := state.RecordLoss()
	if state.LostCount() != 1 {
		t.Errorf("after 1 loss: lostCount = %d, want 1", state.LostCount())
	}
	// First loss applies FadePerFrame once
	expectedFade1 := FadePerFrame
	if math.Abs(fade1-expectedFade1) > 0.001 {
		t.Errorf("after 1 loss: fadeFactor = %f, want %f", fade1, expectedFade1)
	}
}

// TestPLCStateMultipleLosses tests fade decay over multiple losses.
func TestPLCStateMultipleLosses(t *testing.T) {
	state := NewState()

	// Record multiple losses and verify decay
	expectedFade := 1.0
	for i := 1; i <= 5; i++ {
		fade := state.RecordLoss()
		expectedFade *= FadePerFrame

		if state.LostCount() != i {
			t.Errorf("after %d losses: lostCount = %d, want %d", i, state.LostCount(), i)
		}
		if math.Abs(fade-expectedFade) > 0.01 {
			t.Errorf("after %d losses: fadeFactor = %f, want ~%f", i, fade, expectedFade)
		}
	}
}

// TestPLCReset tests PLC state reset after good packet.
func TestPLCReset(t *testing.T) {
	state := NewState()

	// Simulate losses
	state.RecordLoss()
	state.RecordLoss()
	state.RecordLoss()

	if state.LostCount() != 3 {
		t.Errorf("before reset: lostCount = %d, want 3", state.LostCount())
	}

	// Reset (simulating good packet received)
	state.Reset()

	if state.LostCount() != 0 {
		t.Errorf("after reset: lostCount = %d, want 0", state.LostCount())
	}
	if state.FadeFactor() != 1.0 {
		t.Errorf("after reset: fadeFactor = %f, want 1.0", state.FadeFactor())
	}
}

// TestPLCFadeProfile verifies the fade decay profile.
func TestPLCFadeProfile(t *testing.T) {
	state := NewState()

	expected := 1.0
	for i := 0; i < 5; i++ {
		expected *= FadePerFrame
		actual := state.RecordLoss()
		if math.Abs(actual-expected) > 0.001 {
			t.Errorf("loss %d: fadeFactor = %f, want %f", i+1, actual, expected)
		}
	}
}

// TestPLCMaxConcealment tests that output approaches silence after max frames.
func TestPLCMaxConcealment(t *testing.T) {
	state := NewState()

	// Record MaxConcealedFrames losses
	var lastFade float64
	for i := 0; i < MaxConcealedFrames; i++ {
		lastFade = state.RecordLoss()
	}

	// After MaxConcealedFrames (5), fade should be 0.5^5 = 0.03125
	// This is near-silent but not completely zero
	if lastFade > 0.1 {
		t.Errorf("after max concealment: fadeFactor = %f, want < 0.1", lastFade)
	}

	// Continue a few more frames - should approach zero
	state.RecordLoss()
	state.RecordLoss()
	finalFade := state.RecordLoss()

	if finalFade > 0.02 {
		t.Errorf("after extended loss: fadeFactor = %f, want < 0.02", finalFade)
	}

	// State should be exhausted
	if !state.IsExhausted() {
		t.Error("state should be exhausted after many losses")
	}
}

// TestSetLastFrameParams tests parameter storage.
func TestSetLastFrameParams(t *testing.T) {
	state := NewState()

	state.SetLastFrameParams(ModeHybrid, 960, 2)

	if state.Mode() != ModeHybrid {
		t.Errorf("mode = %d, want %d (ModeHybrid)", state.Mode(), ModeHybrid)
	}
	if state.LastFrameSize() != 960 {
		t.Errorf("lastFrameSize = %d, want 960", state.LastFrameSize())
	}
	if state.LastChannels() != 2 {
		t.Errorf("lastChannels = %d, want 2", state.LastChannels())
	}
}

// mockSILKDecoder implements SILKDecoderState for testing.
type mockSILKDecoder struct {
	lpcValues []float32
	lpcOrder  int
	wasVoiced bool
	history   []float32
	histIdx   int
}

func (m *mockSILKDecoder) PrevLPCValues() []float32    { return m.lpcValues }
func (m *mockSILKDecoder) LPCOrder() int               { return m.lpcOrder }
func (m *mockSILKDecoder) IsPreviousFrameVoiced() bool { return m.wasVoiced }
func (m *mockSILKDecoder) OutputHistory() []float32    { return m.history }
func (m *mockSILKDecoder) HistoryIndex() int           { return m.histIdx }

type mockSILKExtendedDecoder struct {
	mockSILKDecoder
	signalType   int
	ltpCoefQ14   [ltpOrder]int16
	pitchLag     int
	lastGainQ16  int32
	ltpScaleQ14  int32
	excitation   []int32
	lpcQ12       []int16
	slpcQ14      []int32
	fsKHz        int
	subfrLength  int
	nbSubfr      int
	ltpMemLength int
	outBufQ0     []int16
}

func (m *mockSILKExtendedDecoder) GetLastSignalType() int        { return m.signalType }
func (m *mockSILKExtendedDecoder) GetLTPCoefficients() [5]int16  { return m.ltpCoefQ14 }
func (m *mockSILKExtendedDecoder) GetPitchLag() int              { return m.pitchLag }
func (m *mockSILKExtendedDecoder) GetLastGain() int32            { return m.lastGainQ16 }
func (m *mockSILKExtendedDecoder) GetLTPScale() int32            { return m.ltpScaleQ14 }
func (m *mockSILKExtendedDecoder) GetExcitationHistory() []int32 { return m.excitation }
func (m *mockSILKExtendedDecoder) GetLPCCoefficientsQ12() []int16 {
	return m.lpcQ12
}
func (m *mockSILKExtendedDecoder) GetSampleRateKHz() int  { return m.fsKHz }
func (m *mockSILKExtendedDecoder) GetSubframeLength() int { return m.subfrLength }
func (m *mockSILKExtendedDecoder) GetNumSubframes() int   { return m.nbSubfr }
func (m *mockSILKExtendedDecoder) GetLTPMemoryLength() int {
	return m.ltpMemLength
}
func (m *mockSILKExtendedDecoder) GetSLPCQ14HistoryQ14() []int32 { return m.slpcQ14 }
func (m *mockSILKExtendedDecoder) GetOutBufHistoryQ0() []int16   { return m.outBufQ0 }

func TestConcealSILKWithLTPLongFrameNoPanic(t *testing.T) {
	dec := &mockSILKExtendedDecoder{
		mockSILKDecoder: mockSILKDecoder{
			lpcValues: make([]float32, 16),
			lpcOrder:  16,
			wasVoiced: true,
			history:   make([]float32, 322),
			histIdx:   200,
		},
		signalType:   2,
		pitchLag:     96,
		lastGainQ16:  65536,
		ltpScaleQ14:  16384,
		excitation:   make([]int32, 640),
		lpcQ12:       make([]int16, 16),
		slpcQ14:      make([]int32, 16),
		fsKHz:        16,
		subfrLength:  80,
		nbSubfr:      4,
		ltpMemLength: 320,
	}
	for i := range dec.history {
		dec.history[i] = float32(math.Sin(float64(i) * 0.09))
	}
	for i := range dec.excitation {
		dec.excitation[i] = int32((i%17)-8) << 8
	}
	for i := range dec.slpcQ14 {
		dec.slpcQ14[i] = int32((i%7)-3) << 12
	}
	dec.lpcQ12[0] = 2048
	dec.lpcQ12[1] = -1024
	dec.ltpCoefQ14 = [ltpOrder]int16{0, 2048, 8192, 2048, 0}

	state := NewSILKPLCState()
	pitchL := []int{96, 96, 96, 96}
	ltpCoefQ14 := make([]int16, ltpOrder*4)
	for sf := 0; sf < 4; sf++ {
		copy(ltpCoefQ14[sf*ltpOrder:(sf+1)*ltpOrder], dec.ltpCoefQ14[:])
	}
	gainsQ16 := []int32{65536, 65536, 65536, 65536}
	lpcQ12 := make([]int16, 16)
	copy(lpcQ12, dec.lpcQ12)
	state.UpdateFromGoodFrame(2, pitchL, ltpCoefQ14, 16384, gainsQ16, lpcQ12, 16, 4, 80)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ConcealSILKWithLTP panicked: %v", r)
		}
	}()

	out := ConcealSILKWithLTP(dec, state, 0, 320)
	if len(out) != 320 {
		t.Fatalf("concealed length = %d, want 320", len(out))
	}
}

func TestConcealSILKWithLTPOutBufPathIgnoresFloatHistory(t *testing.T) {
	newDec := func(historyPhase float64) *mockSILKExtendedDecoder {
		dec := &mockSILKExtendedDecoder{
			mockSILKDecoder: mockSILKDecoder{
				lpcValues: make([]float32, 16),
				lpcOrder:  16,
				wasVoiced: true,
				history:   make([]float32, 322),
				histIdx:   200,
			},
			signalType:   2,
			pitchLag:     96,
			lastGainQ16:  65536,
			ltpScaleQ14:  16384,
			excitation:   make([]int32, 640),
			lpcQ12:       make([]int16, 16),
			slpcQ14:      make([]int32, 16),
			fsKHz:        16,
			subfrLength:  80,
			nbSubfr:      4,
			ltpMemLength: 320,
			outBufQ0:     make([]int16, 320),
		}
		for i := range dec.history {
			dec.history[i] = float32(math.Sin(historyPhase + float64(i)*0.13))
		}
		for i := range dec.outBufQ0 {
			dec.outBufQ0[i] = int16(math.Round(math.Sin(float64(i)*0.09) * 12000.0))
		}
		for i := range dec.excitation {
			dec.excitation[i] = int32((i%19)-9) << 8
		}
		for i := range dec.slpcQ14 {
			dec.slpcQ14[i] = int32((i%9)-4) << 12
		}
		dec.lpcQ12[0] = 2048
		dec.lpcQ12[1] = -1024
		dec.ltpCoefQ14 = [ltpOrder]int16{0, 2048, 8192, 2048, 0}
		return dec
	}

	newState := func(dec *mockSILKExtendedDecoder) *SILKPLCState {
		state := NewSILKPLCState()
		pitchL := []int{dec.pitchLag, dec.pitchLag, dec.pitchLag, dec.pitchLag}
		ltpCoefQ14 := make([]int16, ltpOrder*4)
		for sf := 0; sf < 4; sf++ {
			copy(ltpCoefQ14[sf*ltpOrder:(sf+1)*ltpOrder], dec.ltpCoefQ14[:])
		}
		gainsQ16 := []int32{dec.lastGainQ16, dec.lastGainQ16, dec.lastGainQ16, dec.lastGainQ16}
		lpcQ12 := make([]int16, 16)
		copy(lpcQ12, dec.lpcQ12)
		state.UpdateFromGoodFrame(2, pitchL, ltpCoefQ14, dec.ltpScaleQ14, gainsQ16, lpcQ12, 16, 4, 80)
		return state
	}

	decA := newDec(0.0)
	decB := newDec(1.7) // Different float history, same outBufQ0.

	outA := ConcealSILKWithLTP(decA, newState(decA), 0, 320)
	outB := ConcealSILKWithLTP(decB, newState(decB), 0, 320)

	if len(outA) != len(outB) {
		t.Fatalf("length mismatch: %d vs %d", len(outA), len(outB))
	}
	for i := range outA {
		if outA[i] != outB[i] {
			t.Fatalf("output diverged at sample %d: %d vs %d", i, outA[i], outB[i])
		}
	}
}

// TestSILKPLCOutput tests that SILK PLC produces valid samples.
func TestSILKPLCOutput(t *testing.T) {
	// Create mock decoder with some state
	dec := &mockSILKDecoder{
		lpcValues: make([]float32, 16),
		lpcOrder:  10,
		wasVoiced: false, // Unvoiced mode (comfort noise)
		history:   make([]float32, 322),
		histIdx:   100,
	}

	// Fill history with some values
	for i := range dec.history {
		dec.history[i] = float32(i%100) / 100.0
	}

	frameSize := 320 // 20ms at 16kHz
	fadeFactor := 0.5

	output := ConcealSILK(dec, frameSize, fadeFactor)

	// Check output length
	if len(output) != frameSize {
		t.Errorf("output length = %d, want %d", len(output), frameSize)
	}

	// Check output is not all zeros (should be comfort noise)
	allZero := true
	for _, s := range output {
		if s != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("SILK PLC output should not be all zeros for non-exhausted fade")
	}

	// Check samples are in reasonable range
	for i, s := range output {
		if s > 2.0 || s < -2.0 {
			t.Errorf("sample[%d] = %f, out of range", i, s)
		}
	}
}

// TestSILKPLCVoiced tests SILK PLC for voiced frames.
func TestSILKPLCVoiced(t *testing.T) {
	// Create mock decoder with voiced state
	dec := &mockSILKDecoder{
		lpcValues: make([]float32, 16),
		lpcOrder:  10,
		wasVoiced: true, // Voiced mode (pitch repetition)
		history:   make([]float32, 322),
		histIdx:   200,
	}

	// Fill history with a simple periodic pattern (simulating voiced speech)
	for i := range dec.history {
		dec.history[i] = float32(math.Sin(float64(i) * 0.1))
	}

	frameSize := 160 // 10ms at 16kHz
	fadeFactor := 0.8

	output := ConcealSILK(dec, frameSize, fadeFactor)

	// Check output length
	if len(output) != frameSize {
		t.Errorf("output length = %d, want %d", len(output), frameSize)
	}

	// Voiced PLC should produce non-zero output
	var maxVal float32
	for _, s := range output {
		if abs32(s) > maxVal {
			maxVal = abs32(s)
		}
	}
	if maxVal < 0.001 {
		t.Error("voiced SILK PLC should produce non-trivial output")
	}
}

// TestSILKPLCFadedToSilence tests that zero fade produces silence.
func TestSILKPLCFadedToSilence(t *testing.T) {
	dec := &mockSILKDecoder{
		lpcValues: make([]float32, 16),
		lpcOrder:  10,
		wasVoiced: false,
		history:   make([]float32, 322),
		histIdx:   100,
	}

	frameSize := 320
	fadeFactor := 0.0 // Completely faded

	output := ConcealSILK(dec, frameSize, fadeFactor)

	// All samples should be zero
	for i, s := range output {
		if s != 0 {
			t.Errorf("sample[%d] = %f, want 0 for faded output", i, s)
		}
	}
}

// mockCELTDecoder implements CELTDecoderState and CELTSynthesizer for testing.
type mockCELTDecoder struct {
	channels     int
	prevEnergy   []float64
	rng          uint32
	preemphState []float64
	overlapBuf   []float64
}

func (m *mockCELTDecoder) Channels() int                { return m.channels }
func (m *mockCELTDecoder) PrevEnergy() []float64        { return m.prevEnergy }
func (m *mockCELTDecoder) SetPrevEnergy(e []float64)    { copy(m.prevEnergy, e) }
func (m *mockCELTDecoder) RNG() uint32                  { return m.rng }
func (m *mockCELTDecoder) SetRNG(r uint32)              { m.rng = r }
func (m *mockCELTDecoder) PreemphState() []float64      { return m.preemphState }
func (m *mockCELTDecoder) OverlapBuffer() []float64     { return m.overlapBuf }
func (m *mockCELTDecoder) SetOverlapBuffer(s []float64) { copy(m.overlapBuf, s) }

// Synthesize performs a simple pass-through for testing.
func (m *mockCELTDecoder) Synthesize(coeffs []float64, transient bool, shortBlocks int) []float64 {
	// For testing, just return coefficients scaled down
	output := make([]float64, len(coeffs))
	for i, c := range coeffs {
		output[i] = c * 0.1
	}
	return output
}

// SynthesizeStereo performs a simple pass-through for testing.
func (m *mockCELTDecoder) SynthesizeStereo(coeffsL, coeffsR []float64, transient bool, shortBlocks int) []float64 {
	output := make([]float64, len(coeffsL)*2)
	for i := range coeffsL {
		output[i*2] = coeffsL[i] * 0.1
		output[i*2+1] = coeffsR[i] * 0.1
	}
	return output
}

// TestCELTPLCOutput tests that CELT PLC produces valid samples.
func TestCELTPLCOutput(t *testing.T) {
	// Create mock CELT decoder
	dec := &mockCELTDecoder{
		channels:     1,
		prevEnergy:   make([]float64, 21), // MaxBands
		rng:          22222,
		preemphState: make([]float64, 1),
		overlapBuf:   make([]float64, 120),
	}

	// Set some energy values
	for i := range dec.prevEnergy {
		dec.prevEnergy[i] = -10.0 // ~0.1 linear
	}

	frameSize := 480 // 10ms at 48kHz
	fadeFactor := 0.5

	output := ConcealCELT(dec, dec, frameSize, fadeFactor)

	// Check output length
	if len(output) != frameSize {
		t.Errorf("output length = %d, want %d", len(output), frameSize)
	}

	// Output should not be all zeros
	allZero := true
	for _, s := range output {
		if s != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("CELT PLC output should not be all zeros for non-exhausted fade")
	}
}

// TestCELTPLCEnergyDecay tests that energy decays correctly.
func TestCELTPLCEnergyDecay(t *testing.T) {
	dec := &mockCELTDecoder{
		channels:     1,
		prevEnergy:   make([]float64, 21),
		rng:          22222,
		preemphState: make([]float64, 1),
		overlapBuf:   make([]float64, 120),
	}

	// Set initial energy
	initialEnergy := -10.0
	for i := range dec.prevEnergy {
		dec.prevEnergy[i] = initialEnergy
	}

	frameSize := 480
	fadeFactor := 1.0 // Full fade (for testing energy decay only)

	_ = ConcealCELT(dec, dec, frameSize, fadeFactor)

	// Check energy was decayed
	expectedEnergy := initialEnergy * EnergyDecayPerFrame
	for i, e := range dec.prevEnergy {
		if math.Abs(e-expectedEnergy) > 0.01 {
			t.Errorf("band %d energy = %f, want %f", i, e, expectedEnergy)
		}
	}
}

// TestCELTPLCStereo tests stereo CELT PLC.
func TestCELTPLCStereo(t *testing.T) {
	dec := &mockCELTDecoder{
		channels:     2,
		prevEnergy:   make([]float64, 42), // 21 * 2 channels
		rng:          22222,
		preemphState: make([]float64, 2),
		overlapBuf:   make([]float64, 240), // 120 * 2
	}

	for i := range dec.prevEnergy {
		dec.prevEnergy[i] = -15.0
	}

	frameSize := 960 // 20ms at 48kHz
	fadeFactor := 0.75

	output := ConcealCELT(dec, dec, frameSize, fadeFactor)

	// Check output length (interleaved stereo)
	expectedLen := frameSize * 2
	if len(output) != expectedLen {
		t.Errorf("stereo output length = %d, want %d", len(output), expectedLen)
	}
}

// TestCELTHybridPLC tests hybrid mode CELT PLC (bands 17-21 only).
func TestCELTHybridPLC(t *testing.T) {
	dec := &mockCELTDecoder{
		channels:     1,
		prevEnergy:   make([]float64, 21),
		rng:          22222,
		preemphState: make([]float64, 1),
		overlapBuf:   make([]float64, 120),
	}

	// Set energy - including for high bands (17-21)
	for i := range dec.prevEnergy {
		dec.prevEnergy[i] = -10.0
	}

	frameSize := 960 // 20ms - valid for hybrid
	fadeFactor := 0.5

	output := ConcealCELTHybrid(dec, dec, frameSize, fadeFactor)

	// Check output length
	if len(output) != frameSize {
		t.Errorf("hybrid output length = %d, want %d", len(output), frameSize)
	}
}

// TestNormalizeVector tests vector normalization.
func TestNormalizeVector(t *testing.T) {
	v := []float64{3, 4} // 3-4-5 right triangle
	normalizeVector(v)

	// L2 norm should be 1
	norm := math.Sqrt(v[0]*v[0] + v[1]*v[1])
	if math.Abs(norm-1.0) > 0.0001 {
		t.Errorf("normalized vector norm = %f, want 1.0", norm)
	}
}

// TestGenerateNoiseBand tests noise band generation.
func TestGenerateNoiseBand(t *testing.T) {
	rng := uint32(12345)
	width := 10

	noise := generateNoiseBand(&rng, width)

	if len(noise) != width {
		t.Errorf("noise length = %d, want %d", len(noise), width)
	}

	// Check values are in expected range [-1, 1]
	for i, n := range noise {
		if n < -1.5 || n > 1.5 {
			t.Errorf("noise[%d] = %f, out of range", i, n)
		}
	}

	// RNG should have been advanced
	if rng == 12345 {
		t.Error("RNG was not advanced")
	}
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

// ============================================================================
// LTP-specific tests
// ============================================================================

// TestSILKPLCStateCreation tests SILKPLCState initialization.
func TestSILKPLCStateCreation(t *testing.T) {
	state := NewSILKPLCState()

	// Check default values
	if state.PitchLQ8 == 0 {
		t.Error("PitchLQ8 should not be zero after creation")
	}

	if state.PrevGainQ16[0] != (1 << 16) {
		t.Errorf("PrevGainQ16[0] = %d, want %d", state.PrevGainQ16[0], 1<<16)
	}

	if state.PrevGainQ16[1] != (1 << 16) {
		t.Errorf("PrevGainQ16[1] = %d, want %d", state.PrevGainQ16[1], 1<<16)
	}

	if state.RandScaleQ14 != (1 << 14) {
		t.Errorf("RandScaleQ14 = %d, want %d", state.RandScaleQ14, 1<<14)
	}

	if state.SubfrLength != 80 {
		t.Errorf("SubfrLength = %d, want 80", state.SubfrLength)
	}

	if state.NbSubfr != 4 {
		t.Errorf("NbSubfr = %d, want 4", state.NbSubfr)
	}
}

// TestSILKPLCStateReset tests SILKPLCState reset.
func TestSILKPLCStateReset(t *testing.T) {
	state := NewSILKPLCState()

	// Modify state
	state.PitchLQ8 = 12345
	state.PrevGainQ16[0] = 999
	state.LTPCoefQ14[0] = 100
	state.LastFrameLost = true

	// Reset
	state.Reset(320)

	// Check reset values
	if state.PrevGainQ16[0] != (1 << 16) {
		t.Errorf("After reset: PrevGainQ16[0] = %d, want %d", state.PrevGainQ16[0], 1<<16)
	}

	if state.LTPCoefQ14[0] != 0 {
		t.Errorf("After reset: LTPCoefQ14[0] = %d, want 0", state.LTPCoefQ14[0])
	}

	if state.LastFrameLost {
		t.Error("After reset: LastFrameLost should be false")
	}

	if state.RandScaleQ14 != (1 << 14) {
		t.Errorf("After reset: RandScaleQ14 = %d, want %d", state.RandScaleQ14, 1<<14)
	}
}

// TestSILKPLCStateUpdateFromGoodFrame tests updating PLC state from a good frame.
func TestSILKPLCStateUpdateFromGoodFrame(t *testing.T) {
	state := NewSILKPLCState()

	// Simulate a voiced frame update
	pitchL := []int{100, 101, 102, 103}
	ltpCoefQ14 := make([]int16, 20) // 4 subframes * 5 coefficients
	// Set LTP coefficients for last subframe
	ltpCoefQ14[15] = 2000
	ltpCoefQ14[16] = 3000
	ltpCoefQ14[17] = 4000 // Middle coefficient
	ltpCoefQ14[18] = 3000
	ltpCoefQ14[19] = 2000

	gainsQ16 := []int32{50000, 60000, 70000, 80000}
	lpcQ12 := make([]int16, 16)
	for i := range lpcQ12 {
		lpcQ12[i] = int16(i * 100)
	}

	state.UpdateFromGoodFrame(
		2, // Voiced
		pitchL,
		ltpCoefQ14,
		16384, // 1.0 in Q14
		gainsQ16,
		lpcQ12,
		16,  // fsKHz
		4,   // nbSubfr
		80,  // subfrLength
	)

	// Check that state was updated
	if state.FsKHz != 16 {
		t.Errorf("FsKHz = %d, want 16", state.FsKHz)
	}

	if state.NbSubfr != 4 {
		t.Errorf("NbSubfr = %d, want 4", state.NbSubfr)
	}

	// Check gains were saved
	if state.PrevGainQ16[0] != 70000 {
		t.Errorf("PrevGainQ16[0] = %d, want 70000", state.PrevGainQ16[0])
	}
	if state.PrevGainQ16[1] != 80000 {
		t.Errorf("PrevGainQ16[1] = %d, want 80000", state.PrevGainQ16[1])
	}

	// Check LTP scale was saved
	if state.PrevLTPScaleQ14 != 16384 {
		t.Errorf("PrevLTPScaleQ14 = %d, want 16384", state.PrevLTPScaleQ14)
	}

	// Check LastFrameLost is false
	if state.LastFrameLost {
		t.Error("LastFrameLost should be false after update")
	}
}

// TestSILKPLCStateUnvoicedUpdate tests PLC state update for unvoiced frames.
func TestSILKPLCStateUnvoicedUpdate(t *testing.T) {
	state := NewSILKPLCState()

	pitchL := []int{0, 0, 0, 0}
	ltpCoefQ14 := make([]int16, 20)
	gainsQ16 := []int32{50000, 60000, 70000, 80000}
	lpcQ12 := make([]int16, 10)

	state.UpdateFromGoodFrame(
		1, // Unvoiced
		pitchL,
		ltpCoefQ14,
		0, // No LTP scale for unvoiced
		gainsQ16,
		lpcQ12,
		8,  // fsKHz (NB)
		4,  // nbSubfr
		40, // subfrLength (8kHz * 5ms)
	)

	// Check that LTP coefficients are zero for unvoiced
	for i, coef := range state.LTPCoefQ14 {
		if coef != 0 {
			t.Errorf("LTPCoefQ14[%d] = %d, want 0 for unvoiced", i, coef)
		}
	}

	// Check pitch lag is set to default (18ms * fsKHz)
	expectedPitchLQ8 := int32(8*18) << 8
	if state.PitchLQ8 != expectedPitchLQ8 {
		t.Errorf("PitchLQ8 = %d, want %d for unvoiced", state.PitchLQ8, expectedPitchLQ8)
	}
}

// TestLTPCoeffientClamping tests that LTP gain is clamped to valid range.
func TestLTPCoefficientClamping(t *testing.T) {
	state := NewSILKPLCState()

	// Test with very low LTP gain (should be scaled up)
	pitchL := []int{100, 100, 100, 100}
	ltpCoefQ14 := make([]int16, 20)
	// Set very low LTP gain (below minimum)
	ltpCoefQ14[17] = 1000 // Middle coefficient, below vPitchGainStartMinQ14

	gainsQ16 := []int32{65536, 65536, 65536, 65536}
	lpcQ12 := make([]int16, 16)

	state.UpdateFromGoodFrame(2, pitchL, ltpCoefQ14, 16384, gainsQ16, lpcQ12, 16, 4, 80)

	// The middle coefficient should be scaled up
	middleCoef := state.LTPCoefQ14[ltpOrder/2]
	if middleCoef <= 1000 {
		t.Errorf("LTP coefficient should be scaled up from %d", middleCoef)
	}

	// Test with very high LTP gain (should be scaled down)
	state2 := NewSILKPLCState()
	ltpCoefQ14High := make([]int16, 20)
	// Set very high LTP gain (above maximum)
	ltpCoefQ14High[17] = 20000 // Above vPitchGainStartMaxQ14

	state2.UpdateFromGoodFrame(2, pitchL, ltpCoefQ14High, 16384, gainsQ16, lpcQ12, 16, 4, 80)

	// The middle coefficient should be scaled down
	middleCoef2 := state2.LTPCoefQ14[ltpOrder/2]
	if middleCoef2 >= 20000 {
		t.Errorf("LTP coefficient should be scaled down from %d", middleCoef2)
	}
	if middleCoef2 > int16(vPitchGainStartMaxQ14) {
		t.Errorf("LTP coefficient %d exceeds maximum %d", middleCoef2, vPitchGainStartMaxQ14)
	}
}

// TestFixedPointHelpers tests the fixed-point arithmetic helper functions.
func TestFixedPointHelpers(t *testing.T) {
	// Test silkRand
	seed := int32(12345)
	next := silkRand(seed)
	if next == seed {
		t.Error("silkRand should produce different value")
	}

	// Test smulwb (signed multiply word by bottom half)
	result := smulwb(0x10000, 0x4000) // 1.0 * 0.25 in Q16 * Q14
	if result < 0 || result > 0x4000 {
		t.Errorf("smulwb result out of expected range: %d", result)
	}

	// Test sat16
	if sat16(40000) != 32767 {
		t.Errorf("sat16(40000) = %d, want 32767", sat16(40000))
	}
	if sat16(-40000) != -32768 {
		t.Errorf("sat16(-40000) = %d, want -32768", sat16(-40000))
	}
	if sat16(1000) != 1000 {
		t.Errorf("sat16(1000) = %d, want 1000", sat16(1000))
	}

	// Test rshiftRound
	if rshiftRound(10, 1) != 5 {
		t.Errorf("rshiftRound(10, 1) = %d, want 5", rshiftRound(10, 1))
	}
	if rshiftRound(11, 1) != 6 { // Rounds up
		t.Errorf("rshiftRound(11, 1) = %d, want 6", rshiftRound(11, 1))
	}

	// Test addSat32
	if addSat32(math.MaxInt32, 1) != math.MaxInt32 {
		t.Errorf("addSat32 overflow not handled")
	}
	if addSat32(math.MinInt32, -1) != math.MinInt32 {
		t.Errorf("addSat32 underflow not handled")
	}
}

// TestBwExpandQ12 tests bandwidth expansion.
func TestBwExpandQ12(t *testing.T) {
	ar := []int16{4096, 4096, 4096, 4096} // 1.0 in Q12
	bwExpandQ12(ar, 0.99)

	// Each coefficient should be progressively smaller
	for i := 1; i < len(ar); i++ {
		if ar[i] >= ar[i-1] {
			t.Errorf("bwExpandQ12: ar[%d]=%d should be < ar[%d]=%d", i, ar[i], i-1, ar[i-1])
		}
	}

	// First coefficient should still be close to original * 0.99
	origVal := float64(4096)
	expected := int16(origVal * 0.99)
	if math.Abs(float64(ar[0]-expected)) > 10 {
		t.Errorf("bwExpandQ12: ar[0]=%d, expected ~%d", ar[0], expected)
	}
}

// TestPitchLagDrift tests that pitch lag increases during concealment.
func TestPitchLagDrift(t *testing.T) {
	state := NewSILKPLCState()
	state.PitchLQ8 = 128 << 8 // 128 samples in Q8

	initialPitchLQ8 := state.PitchLQ8

	// Simulate pitch drift
	for i := 0; i < 10; i++ {
		state.PitchLQ8 = state.PitchLQ8 + ((state.PitchLQ8 * pitchDriftFacQ16) >> 16)
	}

	if state.PitchLQ8 <= initialPitchLQ8 {
		t.Errorf("Pitch lag should increase during drift: initial=%d, after=%d",
			initialPitchLQ8, state.PitchLQ8)
	}

	// Check that drift is gradual (about 1% per iteration)
	expectedDrift := float64(initialPitchLQ8) * math.Pow(1.01, 10)
	actualDrift := float64(state.PitchLQ8)
	tolerance := expectedDrift * 0.1 // 10% tolerance

	if math.Abs(actualDrift-expectedDrift) > tolerance {
		t.Errorf("Pitch drift unexpected: got %f, expected ~%f", actualDrift, expectedDrift)
	}
}

// TestAttenuationConstants tests the attenuation constant values.
func TestAttenuationConstants(t *testing.T) {
	// Verify Q15 format values are reasonable (between 0 and 1)
	q15Max := int32(1 << 15) // 1.0 in Q15

	constants := []struct {
		name  string
		value int32
	}{
		{"harmAttQ15_0", harmAttQ15_0},
		{"harmAttQ15_1", harmAttQ15_1},
		{"randAttVQ15_0", randAttVQ15_0},
		{"randAttVQ15_1", randAttVQ15_1},
		{"randAttUVQ15_0", randAttUVQ15_0},
		{"randAttUVQ15_1", randAttUVQ15_1},
	}

	for _, c := range constants {
		if c.value < 0 || c.value > q15Max {
			t.Errorf("%s = %d, should be in range [0, %d]", c.name, c.value, q15Max)
		}

		// Convert to float and verify approximate value
		floatVal := float64(c.value) / float64(q15Max)
		if floatVal < 0.7 || floatVal > 1.0 {
			t.Errorf("%s = %f, expected between 0.7 and 1.0", c.name, floatVal)
		}
	}

	// Verify ordering: first frame attenuation should be less aggressive
	if harmAttQ15_0 < harmAttQ15_1 {
		t.Error("harmAttQ15_0 should be >= harmAttQ15_1")
	}
	if randAttVQ15_0 < randAttVQ15_1 {
		t.Error("randAttVQ15_0 should be >= randAttVQ15_1")
	}
}

// TestLTPGainBounds tests the LTP gain min/max bounds.
func TestLTPGainBounds(t *testing.T) {
	// vPitchGainStartMinQ14 should be 0.7 in Q14
	q14Base := float64(1 << 14)
	minExpected := int32(q14Base * 0.7)
	if math.Abs(float64(vPitchGainStartMinQ14-minExpected)) > 100 {
		t.Errorf("vPitchGainStartMinQ14 = %d, expected ~%d (0.7 in Q14)", vPitchGainStartMinQ14, minExpected)
	}

	// vPitchGainStartMaxQ14 should be 0.95 in Q14
	maxExpected := int32(q14Base * 0.95)
	if math.Abs(float64(vPitchGainStartMaxQ14-maxExpected)) > 100 {
		t.Errorf("vPitchGainStartMaxQ14 = %d, expected ~%d (0.95 in Q14)", vPitchGainStartMaxQ14, maxExpected)
	}

	// Min should be less than max
	if vPitchGainStartMinQ14 >= vPitchGainStartMaxQ14 {
		t.Error("vPitchGainStartMinQ14 should be < vPitchGainStartMaxQ14")
	}
}
