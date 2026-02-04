package encoder

import (
	"math"
	"math/rand"
	"testing"
)

// TestVADStateInit verifies VAD state initialization matches libopus.
func TestVADStateInit(t *testing.T) {
	vad := NewVADState()

	// Check initial counter value
	if vad.Counter != 15 {
		t.Errorf("Counter = %d, want 15", vad.Counter)
	}

	// Check noise level bias initialization (pink noise approximation)
	expectedBias := []int32{50, 25, 16, 12} // VADNoiseLevelsBias / (b+1)
	for b := 0; b < VADNBands; b++ {
		if vad.NoiseLevelBias[b] != expectedBias[b] {
			t.Errorf("NoiseLevelBias[%d] = %d, want %d", b, vad.NoiseLevelBias[b], expectedBias[b])
		}
	}

	// Check initial noise levels (100 * bias)
	for b := 0; b < VADNBands; b++ {
		expectedNL := 100 * vad.NoiseLevelBias[b]
		if vad.NL[b] != expectedNL {
			t.Errorf("NL[%d] = %d, want %d", b, vad.NL[b], expectedNL)
		}
	}

	// Check initial smoothed energy-to-noise ratio (20 dB SNR)
	for b := 0; b < VADNBands; b++ {
		if vad.NrgRatioSmthQ8[b] != 100*256 {
			t.Errorf("NrgRatioSmthQ8[%d] = %d, want %d", b, vad.NrgRatioSmthQ8[b], 100*256)
		}
	}
}

// TestVADSilenceDetection verifies VAD correctly detects silence.
func TestVADSilenceDetection(t *testing.T) {
	vad := NewVADState()

	// Generate silence (very low amplitude noise)
	frameLength := 320 // 20ms at 16kHz
	silence := make([]float32, frameLength)
	for i := range silence {
		silence[i] = 0.0001 * (rand.Float32() - 0.5) // ~-80 dBFS
	}

	// Process multiple frames to let noise estimates stabilize
	var lastActivity int
	var lastIsActive bool
	for i := 0; i < 50; i++ {
		lastActivity, lastIsActive = vad.GetSpeechActivity(silence, frameLength, 16)
	}

	// Should detect as inactive/low activity
	if lastIsActive {
		t.Errorf("Silence detected as active (activity=%d)", lastActivity)
	}

	// Activity level should be low
	if lastActivity > 50 {
		t.Errorf("Activity level too high for silence: %d", lastActivity)
	}
}

// TestVADSpeechDetection verifies VAD correctly detects speech-like signals.
func TestVADSpeechDetection(t *testing.T) {
	vad := NewVADState()

	frameLength := 320 // 20ms at 16kHz

	// First, establish noise floor with silence
	silence := make([]float32, frameLength)
	for i := 0; i < 20; i++ {
		vad.GetSpeechActivity(silence, frameLength, 16)
	}

	// Generate speech-like signal (sine wave with harmonics)
	speech := make([]float32, frameLength)
	for i := range speech {
		t := float64(i) / 16000.0
		// Fundamental + harmonics (speech-like spectrum)
		speech[i] = 0.3 * float32(
			math.Sin(2*math.Pi*200*t)+
				0.5*math.Sin(2*math.Pi*400*t)+
				0.3*math.Sin(2*math.Pi*600*t)+
				0.2*math.Sin(2*math.Pi*1000*t),
		)
	}

	// Process speech frames
	var activity int
	var isActive bool
	for i := 0; i < 10; i++ {
		activity, isActive = vad.GetSpeechActivity(speech, frameLength, 16)
	}

	// Should detect as active
	if !isActive {
		t.Errorf("Speech not detected as active (activity=%d)", activity)
	}

	// Activity level should be high
	if activity < 100 {
		t.Errorf("Activity level too low for speech: %d", activity)
	}
}

// TestVADTransitions verifies smooth transitions between speech and silence.
func TestVADTransitions(t *testing.T) {
	vad := NewVADState()

	frameLength := 320 // 20ms at 16kHz

	// Generate silence and speech signals
	silence := make([]float32, frameLength)
	speech := make([]float32, frameLength)
	for i := range speech {
		t := float64(i) / 16000.0
		speech[i] = 0.3 * float32(math.Sin(2*math.Pi*200*t))
	}

	// Process silence to establish baseline
	for i := 0; i < 30; i++ {
		vad.GetSpeechActivity(silence, frameLength, 16)
	}

	// Transition to speech
	var speechOnsetFrame int
	for i := 0; i < 20; i++ {
		_, isActive := vad.GetSpeechActivity(speech, frameLength, 16)
		if isActive && speechOnsetFrame == 0 {
			speechOnsetFrame = i
		}
	}

	// Speech should be detected within a few frames
	if speechOnsetFrame > 5 {
		t.Errorf("Speech onset detected too late: frame %d", speechOnsetFrame)
	}

	// Transition back to silence
	var silenceOnsetFrame int
	for i := 0; i < 30; i++ {
		_, isActive := vad.GetSpeechActivity(silence, frameLength, 16)
		if !isActive && silenceOnsetFrame == 0 {
			silenceOnsetFrame = i
		}
	}

	// Silence should be detected quickly (no hangover in libopus VAD)
	if silenceOnsetFrame > 2 {
		t.Errorf("Silence detected too late: frame %d", silenceOnsetFrame)
	}
}

// TestVADMultiBandEnergy verifies energy is correctly computed per band.
func TestVADMultiBandEnergy(t *testing.T) {
	vad := NewVADState()

	frameLength := 320 // 20ms at 16kHz

	// Generate signal with energy concentrated in band 3 (4-8 kHz)
	highFreq := make([]float32, frameLength)
	for i := range highFreq {
		t := float64(i) / 16000.0
		highFreq[i] = 0.3 * float32(math.Sin(2*math.Pi*6000*t))
	}

	// Generate signal with energy concentrated in band 0 (0-1 kHz)
	lowFreq := make([]float32, frameLength)
	for i := range lowFreq {
		t := float64(i) / 16000.0
		lowFreq[i] = 0.3 * float32(math.Sin(2*math.Pi*500*t))
	}

	// Process high frequency signal
	vad.Reset()
	for i := 0; i < 20; i++ {
		vad.GetSpeechActivity(highFreq, frameLength, 16)
	}
	highFreqTilt := vad.InputTiltQ15

	// Process low frequency signal
	vad.Reset()
	for i := 0; i < 20; i++ {
		vad.GetSpeechActivity(lowFreq, frameLength, 16)
	}
	lowFreqTilt := vad.InputTiltQ15

	// Low frequency should have positive tilt, high frequency negative
	// (tilt weights favor low frequencies)
	if lowFreqTilt <= highFreqTilt {
		t.Errorf("Tilt measure incorrect: lowFreq=%d, highFreq=%d", lowFreqTilt, highFreqTilt)
	}
}

// TestVADNoiseAdaptation verifies noise level adaptation over time.
func TestVADNoiseAdaptation(t *testing.T) {
	vad := NewVADState()

	frameLength := 320 // 20ms at 16kHz

	// Generate constant low-level noise
	noise := make([]float32, frameLength)
	noiseLevel := float32(0.01) // ~-40 dBFS
	for i := range noise {
		noise[i] = noiseLevel * (rand.Float32() - 0.5)
	}

	// Record initial noise estimate
	initialNL := make([]int32, VADNBands)
	copy(initialNL, vad.NL[:])

	// Process many frames
	for i := 0; i < 100; i++ {
		vad.GetSpeechActivity(noise, frameLength, 16)
	}

	// Noise estimates should have adapted
	noiseChanged := false
	for b := 0; b < VADNBands; b++ {
		if vad.NL[b] != initialNL[b] {
			noiseChanged = true
			break
		}
	}

	if !noiseChanged {
		t.Error("Noise levels did not adapt over time")
	}

	// Counter should have increased
	if vad.Counter <= 15 {
		t.Errorf("Counter did not increase: %d", vad.Counter)
	}
}

// TestAnaFiltBank1 verifies the analysis filter bank splits signals correctly.
func TestAnaFiltBank1(t *testing.T) {
	// Test with a simple signal
	input := make([]int16, 64)
	for i := range input {
		input[i] = int16(1000 * math.Sin(float64(i)*math.Pi/4))
	}

	state := [2]int32{}
	outL := make([]int16, 32)
	outH := make([]int16, 32)

	anaFiltBank1(input, &state, outL, outH, 64)

	// Verify outputs are non-zero
	hasLowOutput := false
	hasHighOutput := false
	for i := 0; i < 32; i++ {
		if outL[i] != 0 {
			hasLowOutput = true
		}
		if outH[i] != 0 {
			hasHighOutput = true
		}
	}

	if !hasLowOutput {
		t.Error("Low band output is all zeros")
	}
	if !hasHighOutput {
		t.Error("High band output is all zeros")
	}

	// State should be updated
	if state[0] == 0 && state[1] == 0 {
		t.Error("Filter state not updated")
	}
}

// TestLin2Log verifies logarithm approximation.
func TestLin2Log(t *testing.T) {
	tests := []struct {
		input    int32
		expected int32 // Approximate expected value
	}{
		{1, 0},
		{2, 128},          // log2(2) = 1 -> 1*128
		{4, 256},          // log2(4) = 2 -> 2*128
		{256, 8 * 128},    // log2(256) = 8
		{1024, 10 * 128},  // log2(1024) = 10
		{65536, 16 * 128}, // log2(65536) = 16
	}

	for _, tt := range tests {
		result := lin2log(tt.input)
		// Allow some tolerance for approximation
		if result < tt.expected-128 || result > tt.expected+128 {
			t.Errorf("lin2log(%d) = %d, expected ~%d", tt.input, result, tt.expected)
		}
	}
}

// TestSigmQ15 verifies sigmoid approximation.
func TestSigmQ15(t *testing.T) {
	// Test high saturation boundary
	// Linear approximation gives 16384 + 127*128 = 32640, which is acceptable
	high := sigmQ15(127)
	if high < 32000 {
		t.Errorf("sigmQ15(127) = %d, want >= 32000", high)
	}

	// Test low value (at VAD_NEGATIVE_OFFSET_Q5)
	if sigmQ15(-128) != 589 {
		t.Errorf("sigmQ15(-128) = %d, want 589", sigmQ15(-128))
	}

	// Test midpoint
	mid := sigmQ15(0)
	if mid < 16000 || mid > 17000 {
		t.Errorf("sigmQ15(0) = %d, expected ~16384", mid)
	}

	// Test monotonicity
	prev := sigmQ15(-128)
	for x := int32(-127); x <= 127; x++ {
		curr := sigmQ15(x)
		if curr < prev {
			t.Errorf("sigmQ15 not monotonic at x=%d: %d < %d", x, curr, prev)
		}
		prev = curr
	}
}

// TestDTXWithVAD verifies DTX integration with multi-band VAD.
func TestDTXWithVAD(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetDTX(true)

	frameSize := 960 // 20ms at 48kHz

	// Generate silence
	silence := make([]float64, frameSize)
	for i := range silence {
		silence[i] = 0.0001 * (rand.Float64() - 0.5)
	}

	// Process many silent frames until DTX activates
	var dtxActivated bool
	for i := 0; i < 50; i++ {
		suppress, _ := enc.shouldUseDTX(silence)
		if suppress {
			dtxActivated = true
			break
		}
	}

	if !dtxActivated {
		t.Error("DTX did not activate after sustained silence")
	}

	// Verify InDTX returns true
	if !enc.InDTX() {
		t.Error("InDTX() returned false after DTX activated")
	}

	// Generate speech-like signal
	speech := make([]float64, frameSize)
	for i := range speech {
		t := float64(i) / 48000.0
		speech[i] = 0.3 * math.Sin(2*math.Pi*200*t)
	}

	// Process speech - should exit DTX
	for i := 0; i < 5; i++ {
		enc.shouldUseDTX(speech)
	}

	if enc.InDTX() {
		t.Error("DTX did not deactivate after speech")
	}
}

// TestVADDifferentSampleRates verifies VAD works at different sample rates.
func TestVADDifferentSampleRates(t *testing.T) {
	sampleRates := []struct {
		fsKHz       int
		frameLength int
	}{
		{8, 160},  // 20ms at 8kHz
		{12, 240}, // 20ms at 12kHz
		{16, 320}, // 20ms at 16kHz
	}

	for _, sr := range sampleRates {
		t.Run(string(rune(sr.fsKHz))+"kHz", func(t *testing.T) {
			vad := NewVADState()

			// Generate speech-like signal at this sample rate
			speech := make([]float32, sr.frameLength)
			for i := range speech {
				t := float64(i) / float64(sr.fsKHz*1000)
				speech[i] = 0.3 * float32(math.Sin(2*math.Pi*200*t))
			}

			// Should detect activity
			var activity int
			for i := 0; i < 20; i++ {
				activity, _ = vad.GetSpeechActivity(speech, sr.frameLength, sr.fsKHz)
			}

			if activity < 50 {
				t.Errorf("Low activity (%d) at %dkHz", activity, sr.fsKHz)
			}
		})
	}
}

// TestVADConstants verifies VAD constants match libopus.
func TestVADConstants(t *testing.T) {
	// Verify constants match libopus silk/define.h
	if VADNBands != 4 {
		t.Errorf("VADNBands = %d, want 4", VADNBands)
	}
	if VADInternalSubframes != 4 {
		t.Errorf("VADInternalSubframes = %d, want 4", VADInternalSubframes)
	}
	if VADNoiseLevelSmoothCoefQ16 != 1024 {
		t.Errorf("VADNoiseLevelSmoothCoefQ16 = %d, want 1024", VADNoiseLevelSmoothCoefQ16)
	}
	if VADNoiseLevelsBias != 50 {
		t.Errorf("VADNoiseLevelsBias = %d, want 50", VADNoiseLevelsBias)
	}
	if VADNegativeOffsetQ5 != 128 {
		t.Errorf("VADNegativeOffsetQ5 = %d, want 128", VADNegativeOffsetQ5)
	}
	if VADSNRFactorQ16 != 45000 {
		t.Errorf("VADSNRFactorQ16 = %d, want 45000", VADSNRFactorQ16)
	}
}

// BenchmarkVAD measures VAD processing performance.
func BenchmarkVAD(b *testing.B) {
	vad := NewVADState()
	frameLength := 320 // 20ms at 16kHz

	// Generate test signal
	signal := make([]float32, frameLength)
	for i := range signal {
		t := float64(i) / 16000.0
		signal[i] = 0.3 * float32(math.Sin(2*math.Pi*200*t))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vad.GetSpeechActivity(signal, frameLength, 16)
	}
}

// BenchmarkAnaFiltBank measures filter bank performance.
func BenchmarkAnaFiltBank(b *testing.B) {
	input := make([]int16, 640)
	for i := range input {
		input[i] = int16(1000 * math.Sin(float64(i)*math.Pi/4))
	}

	state := [2]int32{}
	outL := make([]int16, 320)
	outH := make([]int16, 320)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		anaFiltBank1(input, &state, outL, outH, 640)
	}
}
