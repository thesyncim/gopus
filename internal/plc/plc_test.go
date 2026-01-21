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

	// Expected fade values after each loss
	expectedFades := []float64{
		0.5,    // 1 loss: 1.0 * 0.5
		0.25,   // 2 losses: 0.5 * 0.5
		0.125,  // 3 losses: 0.25 * 0.5
		0.0625, // 4 losses: 0.125 * 0.5
		0.03125, // 5 losses: 0.0625 * 0.5
	}

	for i, expected := range expectedFades {
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

	if finalFade > 0.01 {
		t.Errorf("after extended loss: fadeFactor = %f, want < 0.01", finalFade)
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

func (m *mockSILKDecoder) PrevLPCValues() []float32       { return m.lpcValues }
func (m *mockSILKDecoder) LPCOrder() int                  { return m.lpcOrder }
func (m *mockSILKDecoder) IsPreviousFrameVoiced() bool    { return m.wasVoiced }
func (m *mockSILKDecoder) OutputHistory() []float32       { return m.history }
func (m *mockSILKDecoder) HistoryIndex() int              { return m.histIdx }

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
