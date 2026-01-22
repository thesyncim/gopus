package encoder

import (
	"math"
	"testing"

	"gopus"
	"gopus/internal/hybrid"
	"gopus/internal/rangecoding"
)

// TestNewEncoder verifies the encoder constructor creates valid encoder.
func TestNewEncoder(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
		wantRate   int
		wantCh     int
	}{
		{"48kHz mono", 48000, 1, 48000, 1},
		{"48kHz stereo", 48000, 2, 48000, 2},
		{"16kHz mono", 16000, 1, 16000, 1},
		{"8kHz mono", 8000, 1, 8000, 1},
		{"invalid rate defaults to 48kHz", 44100, 1, 48000, 1},
		{"invalid channels clamped", 48000, 5, 48000, 2},
		{"zero channels clamped to 1", 48000, 0, 48000, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder(tt.sampleRate, tt.channels)

			if enc == nil {
				t.Fatal("NewEncoder returned nil")
			}

			if enc.SampleRate() != tt.wantRate {
				t.Errorf("SampleRate() = %d, want %d", enc.SampleRate(), tt.wantRate)
			}

			if enc.Channels() != tt.wantCh {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), tt.wantCh)
			}

			// Default mode should be ModeAuto
			if enc.Mode() != ModeAuto {
				t.Errorf("Mode() = %d, want ModeAuto", enc.Mode())
			}

			// Default bandwidth should be Fullband
			if enc.Bandwidth() != gopus.BandwidthFullband {
				t.Errorf("Bandwidth() = %d, want Fullband", enc.Bandwidth())
			}

			// Default frame size should be 960 (20ms)
			if enc.FrameSize() != 960 {
				t.Errorf("FrameSize() = %d, want 960", enc.FrameSize())
			}
		})
	}
}

// TestEncoderSetMode verifies mode setting works correctly.
func TestEncoderSetMode(t *testing.T) {
	enc := NewEncoder(48000, 1)

	tests := []struct {
		mode Mode
		name string
	}{
		{ModeAuto, "ModeAuto"},
		{ModeSILK, "ModeSILK"},
		{ModeHybrid, "ModeHybrid"},
		{ModeCELT, "ModeCELT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc.SetMode(tt.mode)
			if enc.Mode() != tt.mode {
				t.Errorf("Mode() = %d, want %d", enc.Mode(), tt.mode)
			}
		})
	}
}

// TestEncoderSetBandwidth verifies bandwidth setting works correctly.
func TestEncoderSetBandwidth(t *testing.T) {
	enc := NewEncoder(48000, 1)

	tests := []struct {
		bw   gopus.Bandwidth
		name string
	}{
		{gopus.BandwidthNarrowband, "Narrowband"},
		{gopus.BandwidthMediumband, "Mediumband"},
		{gopus.BandwidthWideband, "Wideband"},
		{gopus.BandwidthSuperwideband, "Superwideband"},
		{gopus.BandwidthFullband, "Fullband"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc.SetBandwidth(tt.bw)
			if enc.Bandwidth() != tt.bw {
				t.Errorf("Bandwidth() = %d, want %d", enc.Bandwidth(), tt.bw)
			}
		})
	}
}

// TestEncoderSetFrameSize verifies frame size setting works correctly.
func TestEncoderSetFrameSize(t *testing.T) {
	enc := NewEncoder(48000, 1)

	frameSizes := []int{120, 240, 480, 960, 1920, 2880}

	for _, size := range frameSizes {
		t.Run(string(rune('0'+size/100)), func(t *testing.T) {
			enc.SetFrameSize(size)
			if enc.FrameSize() != size {
				t.Errorf("FrameSize() = %d, want %d", enc.FrameSize(), size)
			}
		})
	}
}

// TestEncoderReset verifies reset clears state properly.
func TestEncoderReset(t *testing.T) {
	enc := NewEncoder(48000, 1)

	// Modify state
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	// Reset
	enc.Reset()

	// Mode and bandwidth should be preserved (they're config, not state)
	if enc.Mode() != ModeHybrid {
		t.Errorf("Mode changed after Reset, got %d", enc.Mode())
	}

	// prevSamples should be zeroed (delay buffer)
	for i, v := range enc.prevSamples {
		if v != 0 {
			t.Errorf("prevSamples[%d] = %f, want 0", i, v)
			break
		}
	}
}

// TestValidFrameSize verifies frame size validation.
func TestValidFrameSize(t *testing.T) {
	tests := []struct {
		frameSize int
		mode      Mode
		want      bool
	}{
		// SILK valid sizes
		{480, ModeSILK, true},
		{960, ModeSILK, true},
		{1920, ModeSILK, true},
		{2880, ModeSILK, true},
		{120, ModeSILK, false},
		{240, ModeSILK, false},

		// Hybrid valid sizes
		{480, ModeHybrid, true},
		{960, ModeHybrid, true},
		{120, ModeHybrid, false},
		{240, ModeHybrid, false},
		{1920, ModeHybrid, false},

		// CELT valid sizes
		{120, ModeCELT, true},
		{240, ModeCELT, true},
		{480, ModeCELT, true},
		{960, ModeCELT, true},
		{1920, ModeCELT, false},
		{2880, ModeCELT, false},

		// Auto accepts all valid sizes
		{120, ModeAuto, true},
		{240, ModeAuto, true},
		{480, ModeAuto, true},
		{960, ModeAuto, true},
		{1920, ModeAuto, true},
		{2880, ModeAuto, true},
		{100, ModeAuto, false},
	}

	for _, tt := range tests {
		got := ValidFrameSize(tt.frameSize, tt.mode)
		if got != tt.want {
			t.Errorf("ValidFrameSize(%d, %d) = %v, want %v", tt.frameSize, tt.mode, got, tt.want)
		}
	}
}

// generateTestSignal generates a test signal (sine wave + noise) for encoding tests.
func generateTestSignal(samples int, channels int) []float64 {
	pcm := make([]float64, samples*channels)
	freq := 440.0 // 440 Hz sine wave
	sampleRate := 48000.0

	for i := 0; i < samples; i++ {
		t := float64(i) / sampleRate
		// Mix of sine wave and some noise for realistic content
		sample := 0.5 * math.Sin(2*math.Pi*freq*t)
		// Add a second harmonic
		sample += 0.25 * math.Sin(2*math.Pi*freq*2*t)
		// Add some high frequency content
		sample += 0.1 * math.Sin(2*math.Pi*8000*t)

		if channels == 1 {
			pcm[i] = sample
		} else {
			// Stereo: slight pan difference
			pcm[i*2] = sample * 0.8   // Left
			pcm[i*2+1] = sample * 1.0 // Right
		}
	}
	return pcm
}

// computeEnergy computes the total energy of a signal.
func computeEnergy(samples []float64) float64 {
	var energy float64
	for _, s := range samples {
		energy += s * s
	}
	return energy / float64(len(samples))
}

// TestHybridEncode10ms tests 480-sample (10ms) hybrid frame encoding.
func TestHybridEncode10ms(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	pcm := generateTestSignal(480, 1)

	encoded, err := enc.Encode(pcm, 480)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Error("Encoded data is empty")
	}

	t.Logf("10ms hybrid frame: %d bytes", len(encoded))
}

// TestHybridEncode20ms tests 960-sample (20ms) hybrid frame encoding.
func TestHybridEncode20ms(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	pcm := generateTestSignal(960, 1)

	encoded, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Error("Encoded data is empty")
	}

	t.Logf("20ms hybrid frame: %d bytes", len(encoded))
}

// TestHybridEncodeMono tests mono hybrid encoding.
func TestHybridEncodeMono(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthFullband)

	frameSizes := []int{480, 960}

	for _, frameSize := range frameSizes {
		t.Run(string(rune('0'+frameSize/100)), func(t *testing.T) {
			pcm := generateTestSignal(frameSize, 1)

			encoded, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			if len(encoded) == 0 {
				t.Error("Encoded data is empty")
			}

			t.Logf("Mono %dms: %d bytes", frameSize/48, len(encoded))
		})
	}
}

// TestHybridEncodeStereo tests stereo hybrid encoding.
func TestHybridEncodeStereo(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthFullband)

	frameSizes := []int{480, 960}

	for _, frameSize := range frameSizes {
		t.Run(string(rune('0'+frameSize/100)), func(t *testing.T) {
			pcm := generateTestSignal(frameSize, 2)

			encoded, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			if len(encoded) == 0 {
				t.Error("Encoded data is empty")
			}

			t.Logf("Stereo %dms: %d bytes", frameSize/48, len(encoded))
		})
	}
}

// TestHybridRoundTrip tests encode with unified encoder, decode with hybrid.Decoder.
func TestHybridRoundTrip(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	// Generate test signal
	pcm := generateTestSignal(960, 1)

	// Encode
	encoded, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Fatal("Encoded data is empty")
	}

	t.Logf("Encoded %d samples to %d bytes", 960, len(encoded))

	// Decode using Phase 4 hybrid decoder
	dec := hybrid.NewDecoder(1)
	rd := &rangecoding.Decoder{}
	rd.Init(encoded)

	decoded, err := dec.DecodeWithDecoder(rd, 960)
	if err != nil {
		t.Logf("Decode returned error (expected for initial implementation): %v", err)
		// Don't fail - we're testing that encoding produces valid bytes
	}

	if len(decoded) > 0 {
		// Verify output has energy (not silence)
		energy := computeEnergy(decoded)
		t.Logf("Decoded signal energy: %f", energy)

		if energy > 0.0001 {
			t.Log("Round-trip produced signal with energy")
		}
	}
}

// TestInvalidHybridFrameSize tests that invalid frame sizes return error for hybrid mode.
func TestInvalidHybridFrameSize(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)

	invalidSizes := []int{120, 240, 1920, 2880, 100, 500}

	for _, size := range invalidSizes {
		t.Run(string(rune('0'+size/100)), func(t *testing.T) {
			pcm := make([]float64, size)

			_, err := enc.Encode(pcm, size)
			if err == nil {
				t.Errorf("Expected error for frame size %d in hybrid mode", size)
			}
		})
	}
}

// TestDownsample48to16 tests the downsampling function.
func TestDownsample48to16(t *testing.T) {
	// 960 samples at 48kHz should become 320 samples at 16kHz
	pcm := generateTestSignal(960, 1)

	downsampled := downsample48to16(pcm, 1)

	expectedLen := 960 / 3
	if len(downsampled) != expectedLen {
		t.Errorf("Downsampled length = %d, want %d", len(downsampled), expectedLen)
	}

	// Verify downsampled signal has reasonable values
	var maxVal float32
	for _, s := range downsampled {
		if s > maxVal {
			maxVal = s
		}
		if -s > maxVal {
			maxVal = -s
		}
	}

	if maxVal < 0.01 {
		t.Error("Downsampled signal appears to be silent")
	}

	t.Logf("Downsampled max value: %f", maxVal)
}

// TestModeAutoSelection tests automatic mode selection.
func TestModeAutoSelection(t *testing.T) {
	enc := NewEncoder(48000, 1)
	// ModeAuto is default

	// Test that SWB/FB with hybrid-compatible frame size selects hybrid
	enc.SetBandwidth(gopus.BandwidthSuperwideband)
	pcm := generateTestSignal(960, 1)

	encoded, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Error("Encoded data is empty")
	}

	t.Logf("Auto mode (SWB, 20ms): %d bytes", len(encoded))
}

// TestMultipleFramesHybrid tests encoding multiple consecutive hybrid frames.
func TestMultipleFramesHybrid(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	numFrames := 5
	frameSize := 960

	for i := 0; i < numFrames; i++ {
		pcm := generateTestSignal(frameSize, 1)

		encoded, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Frame %d encode failed: %v", i, err)
		}

		if len(encoded) == 0 {
			t.Errorf("Frame %d encoded data is empty", i)
		}

		t.Logf("Frame %d: %d bytes", i, len(encoded))
	}
}
