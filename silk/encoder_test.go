package silk

import (
	"math"
	"testing"
)

func TestNewEncoder(t *testing.T) {
	tests := []struct {
		name           string
		bandwidth      Bandwidth
		wantLPCOrder   int
		wantSampleRate int
	}{
		{"narrowband", BandwidthNarrowband, 10, 8000},
		{"mediumband", BandwidthMediumband, 10, 12000},
		{"wideband", BandwidthWideband, 16, 16000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder(tt.bandwidth)

			if enc.LPCOrder() != tt.wantLPCOrder {
				t.Errorf("LPCOrder() = %d, want %d", enc.LPCOrder(), tt.wantLPCOrder)
			}
			if enc.SampleRate() != tt.wantSampleRate {
				t.Errorf("SampleRate() = %d, want %d", enc.SampleRate(), tt.wantSampleRate)
			}
			if enc.Bandwidth() != tt.bandwidth {
				t.Errorf("Bandwidth() = %v, want %v", enc.Bandwidth(), tt.bandwidth)
			}
			if enc.HaveEncoded() {
				t.Error("HaveEncoded() should be false for new encoder")
			}
			if len(enc.PrevLSFQ15()) != tt.wantLPCOrder {
				t.Errorf("PrevLSFQ15() length = %d, want %d", len(enc.PrevLSFQ15()), tt.wantLPCOrder)
			}
			if enc.pitchState.prevLag != 0 {
				t.Errorf("pitchState.prevLag = %d, want 0", enc.pitchState.prevLag)
			}
			if enc.nsqState == nil || enc.nsqState.lagPrev != 0 {
				t.Errorf("nsqState.lagPrev = %d, want 0", enc.nsqState.lagPrev)
			}
		})
	}
}

func TestEncoderReset(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Modify state
	enc.MarkEncoded()
	enc.SetPreviousLogGain(100)
	enc.SetPreviousFrameVoiced(true)
	enc.SetPrevStereoWeights([2]int16{1000, 2000})

	// Verify modifications
	if !enc.HaveEncoded() {
		t.Error("expected HaveEncoded after MarkEncoded")
	}
	if enc.PreviousLogGain() != 100 {
		t.Error("expected PreviousLogGain = 100")
	}

	// Reset
	enc.Reset()

	// Verify reset
	if enc.HaveEncoded() {
		t.Error("HaveEncoded should be false after Reset")
	}
	if enc.PreviousLogGain() != 0 {
		t.Error("PreviousLogGain should be 0 after Reset")
	}
	if enc.IsPreviousFrameVoiced() {
		t.Error("IsPreviousFrameVoiced should be false after Reset")
	}
	weights := enc.PrevStereoWeights()
	if weights[0] != 0 || weights[1] != 0 {
		t.Error("PrevStereoWeights should be [0,0] after Reset")
	}
	if enc.pitchState.prevLag != 0 {
		t.Errorf("pitchState.prevLag should be 0 after Reset, got %d", enc.pitchState.prevLag)
	}
	if enc.nsqState == nil || enc.nsqState.lagPrev != 0 {
		t.Errorf("nsqState.lagPrev should be 0 after Reset, got %d", enc.nsqState.lagPrev)
	}
}

func TestEncoderStateAccessors(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Test gain accessors
	enc.SetPreviousLogGain(42)
	if enc.PreviousLogGain() != 42 {
		t.Errorf("PreviousLogGain = %d, want 42", enc.PreviousLogGain())
	}

	// Test voiced accessors
	enc.SetPreviousFrameVoiced(true)
	if !enc.IsPreviousFrameVoiced() {
		t.Error("IsPreviousFrameVoiced should be true")
	}

	// Test LSF accessors
	lsf := make([]int16, 16)
	for i := range lsf {
		lsf[i] = int16(i * 100)
	}
	enc.SetPrevLSFQ15(lsf)
	result := enc.PrevLSFQ15()
	for i := range lsf {
		if result[i] != lsf[i] {
			t.Errorf("PrevLSFQ15[%d] = %d, want %d", i, result[i], lsf[i])
		}
	}

	// Test stereo weight accessors
	enc.SetPrevStereoWeights([2]int16{1234, 5678})
	weights := enc.PrevStereoWeights()
	if weights[0] != 1234 || weights[1] != 5678 {
		t.Errorf("PrevStereoWeights = %v, want [1234, 5678]", weights)
	}
}

func TestClassifyFrame(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Test inactive (silence)
	silence := make([]float32, 320)
	sigType, _ := enc.classifyFrame(silence)
	if sigType != 0 {
		t.Errorf("silence should be inactive, got signalType=%d", sigType)
	}

	// Test unvoiced (noise-like)
	// Create signal with multiple non-harmonic frequencies
	noise := make([]float32, 320)
	for i := range noise {
		// Sum of incommensurate frequencies creates noise-like signal
		noise[i] = float32(math.Sin(float64(i)*0.1) + math.Sin(float64(i)*0.37) + math.Sin(float64(i)*0.73))
		noise[i] *= 1000
	}
	sigType, _ = enc.classifyFrame(noise)
	if sigType == 0 {
		t.Errorf("noise should be active, got signalType=0")
	}

	// Test voiced (sinusoid = periodic)
	voiced := make([]float32, 320)
	freq := 200.0 // 200 Hz fundamental
	for i := range voiced {
		voiced[i] = float32(math.Sin(2*math.Pi*freq*float64(i)/16000.0)) * (10000 * int16Scale)
	}
	sigType, _ = enc.classifyFrame(voiced)
	if sigType != 2 {
		t.Errorf("periodic signal should be voiced, got signalType=%d", sigType)
	}
}

func TestClassifyFrameEmptyInput(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Empty input should return inactive
	sigType, quantOffset := enc.classifyFrame(nil)
	if sigType != 0 || quantOffset != 0 {
		t.Errorf("empty input: got signalType=%d, quantOffset=%d, want 0, 0", sigType, quantOffset)
	}

	sigType, quantOffset = enc.classifyFrame([]float32{})
	if sigType != 0 || quantOffset != 0 {
		t.Errorf("empty slice: got signalType=%d, quantOffset=%d, want 0, 0", sigType, quantOffset)
	}
}

func TestClassifyFrameQuantOffset(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// High periodicity should give high quant offset
	voiced := make([]float32, 320)
	freq := 150.0 // Strong periodic signal
	for i := range voiced {
		voiced[i] = float32(math.Sin(2*math.Pi*freq*float64(i)/16000.0)) * (10000 * int16Scale)
	}
	_, quantOffset := enc.classifyFrame(voiced)
	if quantOffset != 1 {
		t.Errorf("high periodicity signal should have quantOffset=1, got %d", quantOffset)
	}
}

func TestComputePeriodicity(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Test with periodic signal
	periodic := make([]float32, 320)
	period := 80 // 80 samples = 200 Hz at 16kHz
	for i := range periodic {
		periodic[i] = float32(math.Sin(2 * math.Pi * float64(i) / float64(period)))
	}
	periodicity := enc.computePeriodicity(periodic, 32, 288)
	if periodicity < 0.9 {
		t.Errorf("periodic signal periodicity = %f, want >= 0.9", periodicity)
	}

	// Test with noise (low periodicity)
	// Use irrational-ratio frequencies to minimize autocorrelation peaks
	noise := make([]float32, 320)
	phi := (1 + math.Sqrt(5)) / 2 // Golden ratio for maximally aperiodic pattern
	for i := range noise {
		// Combine multiple incommensurate frequencies
		noise[i] = float32(
			math.Sin(float64(i)*0.01*phi) +
				math.Sin(float64(i)*0.02*phi*phi) +
				math.Sin(float64(i)*0.03*phi*phi*phi) +
				math.Sin(float64(i)*0.05))
	}
	periodicity = enc.computePeriodicity(noise, 32, 288)
	// Noise should have lower periodicity than voiced speech
	// Allow some correlation due to signal smoothness
	if periodicity > 0.95 {
		t.Errorf("noise signal periodicity = %f, want < 0.95", periodicity)
	}
}

func TestComputePeriodicityEdgeCases(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Empty signal
	periodicity := enc.computePeriodicity([]float32{}, 1, 10)
	if periodicity != 0 {
		t.Errorf("empty signal periodicity = %f, want 0", periodicity)
	}

	// Short signal (shorter than minLag)
	short := []float32{1, 2, 3, 4, 5}
	periodicity = enc.computePeriodicity(short, 10, 20)
	if periodicity != 0 {
		t.Errorf("short signal periodicity = %f, want 0", periodicity)
	}

	// Invalid lag range
	signal := make([]float32, 100)
	periodicity = enc.computePeriodicity(signal, 50, 10) // minLag > maxLag
	if periodicity != 0 {
		t.Errorf("invalid lag range periodicity = %f, want 0", periodicity)
	}
}
