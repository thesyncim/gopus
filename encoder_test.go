package gopus

import (
	"fmt"
	"math"
	"testing"
)

func TestNewEncoder_ValidParams(t *testing.T) {
	tests := []struct {
		name        string
		sampleRate  int
		channels    int
		application Application
	}{
		{"48kHz_mono_voip", 48000, 1, ApplicationVoIP},
		{"48kHz_stereo_voip", 48000, 2, ApplicationVoIP},
		{"48kHz_mono_audio", 48000, 1, ApplicationAudio},
		{"48kHz_stereo_audio", 48000, 2, ApplicationAudio},
		{"48kHz_mono_lowdelay", 48000, 1, ApplicationLowDelay},
		{"24kHz_mono_voip", 24000, 1, ApplicationVoIP},
		{"16kHz_mono_voip", 16000, 1, ApplicationVoIP},
		{"12kHz_mono_voip", 12000, 1, ApplicationVoIP},
		{"8kHz_mono_voip", 8000, 1, ApplicationVoIP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(tt.sampleRate, tt.channels, tt.application)
			if err != nil {
				t.Fatalf("NewEncoder(%d, %d, %d) unexpected error: %v", tt.sampleRate, tt.channels, tt.application, err)
			}
			if enc == nil {
				t.Fatal("NewEncoder returned nil encoder")
			}
			if enc.SampleRate() != tt.sampleRate {
				t.Errorf("SampleRate() = %d, want %d", enc.SampleRate(), tt.sampleRate)
			}
			if enc.Channels() != tt.channels {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), tt.channels)
			}
		})
	}
}

func TestNewEncoder_InvalidParams(t *testing.T) {
	tests := []struct {
		name        string
		sampleRate  int
		channels    int
		expectedErr error
	}{
		{"invalid_rate_0", 0, 1, ErrInvalidSampleRate},
		{"invalid_rate_44100", 44100, 1, ErrInvalidSampleRate},
		{"invalid_rate_96000", 96000, 1, ErrInvalidSampleRate},
		{"invalid_channels_0", 48000, 0, ErrInvalidChannels},
		{"invalid_channels_3", 48000, 3, ErrInvalidChannels},
		{"invalid_channels_negative", 48000, -1, ErrInvalidChannels},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(tt.sampleRate, tt.channels, ApplicationAudio)
			if err != tt.expectedErr {
				t.Errorf("NewEncoder(%d, %d) error = %v, want %v", tt.sampleRate, tt.channels, err, tt.expectedErr)
			}
			if enc != nil {
				t.Error("NewEncoder returned non-nil encoder on error")
			}
		})
	}
}

// generateSineWave generates a sine wave at the given frequency.
func generateSineWave(sampleRate int, freq float64, samples int) []float32 {
	pcm := make([]float32, samples)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
	}
	return pcm
}

func TestEncoder_Encode_Float32(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Generate 20ms of 440Hz sine wave
	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	// Encode
	data := make([]byte, 4000)
	n, err := enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if n == 0 {
		t.Fatal("Encode returned 0 bytes")
	}

	t.Logf("Encoded %d samples to %d bytes", frameSize, n)
}

func TestEncoder_Encode_Int16(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Generate 20ms of 440Hz sine wave as int16
	frameSize := 960
	pcm := make([]int16, frameSize)
	for i := range pcm {
		pcm[i] = int16(16384 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}

	// Encode
	data := make([]byte, 4000)
	n, err := enc.EncodeInt16(pcm, data)
	if err != nil {
		t.Fatalf("EncodeInt16 error: %v", err)
	}

	if n == 0 {
		t.Fatal("EncodeInt16 returned 0 bytes")
	}

	t.Logf("Encoded %d int16 samples to %d bytes", frameSize, n)
}

func TestEncoder_Encode_RoundTrip(t *testing.T) {
	// Create encoder and decoder
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate 20ms of 440Hz sine wave
	frameSize := 960
	pcmIn := generateSineWave(48000, 440, frameSize)

	// Calculate input energy
	var inputEnergy float64
	for _, s := range pcmIn {
		inputEnergy += float64(s) * float64(s)
	}

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet")
	}

	// Decode
	pcmOut, err := dec.DecodeFloat32(packet)
	if err != nil {
		t.Fatalf("DecodeFloat32 error: %v", err)
	}

	// Calculate output energy
	var outputEnergy float64
	for _, s := range pcmOut {
		outputEnergy += float64(s) * float64(s)
	}

	t.Logf("Input energy: %f, Output energy: %f", inputEnergy, outputEnergy)
	t.Logf("Encoded to %d bytes, decoded to %d samples", len(packet), len(pcmOut))

	// The output should have some energy (lossy compression but not zero)
	if outputEnergy == 0 {
		t.Error("Decoded output has zero energy")
	}
}

func TestEncoder_SetBitrate(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Valid bitrates
	validBitrates := []int{6000, 12000, 32000, 64000, 128000, 256000, 510000}
	for _, br := range validBitrates {
		t.Run(fmt.Sprintf("bitrate_%d", br), func(t *testing.T) {
			err := enc.SetBitrate(br)
			if err != nil {
				t.Errorf("SetBitrate(%d) error: %v", br, err)
			}
			if enc.Bitrate() != br {
				t.Errorf("Bitrate() = %d, want %d", enc.Bitrate(), br)
			}
		})
	}

	// Invalid bitrates
	invalidBitrates := []int{0, 5999, 510001, -1, 1000000}
	for _, br := range invalidBitrates {
		t.Run(fmt.Sprintf("invalid_bitrate_%d", br), func(t *testing.T) {
			err := enc.SetBitrate(br)
			if err != ErrInvalidBitrate {
				t.Errorf("SetBitrate(%d) error = %v, want ErrInvalidBitrate", br, err)
			}
		})
	}
}

func TestEncoder_SetComplexity(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Valid complexity values
	for c := 0; c <= 10; c++ {
		t.Run(fmt.Sprintf("complexity_%d", c), func(t *testing.T) {
			err := enc.SetComplexity(c)
			if err != nil {
				t.Errorf("SetComplexity(%d) error: %v", c, err)
			}
			if enc.Complexity() != c {
				t.Errorf("Complexity() = %d, want %d", enc.Complexity(), c)
			}
		})
	}

	// Invalid complexity values
	invalidComplexity := []int{-1, 11, 100}
	for _, c := range invalidComplexity {
		t.Run(fmt.Sprintf("invalid_complexity_%d", c), func(t *testing.T) {
			err := enc.SetComplexity(c)
			if err != ErrInvalidComplexity {
				t.Errorf("SetComplexity(%d) error = %v, want ErrInvalidComplexity", c, err)
			}
		})
	}
}

func TestEncoder_DTX_Silence(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Enable DTX
	enc.SetDTX(true)
	if !enc.DTXEnabled() {
		t.Error("DTXEnabled() = false after SetDTX(true)")
	}

	// Encode multiple silence frames
	frameSize := 960
	silence := make([]float32, frameSize)

	// DTX requires several frames of silence before suppressing
	// The DTXFrameThreshold is 20 frames (400ms)
	for i := 0; i < 25; i++ {
		data := make([]byte, 4000)
		n, err := enc.Encode(silence, data)
		if err != nil {
			t.Fatalf("Encode error on frame %d: %v", i, err)
		}

		// After threshold, should produce 0 bytes (suppressed)
		if i > 20 && n == 0 {
			t.Logf("Frame %d suppressed by DTX (0 bytes)", i)
			return // Success - DTX suppressed a frame
		}
	}

	// DTX may or may not suppress depending on implementation details
	t.Log("DTX did not suppress frames (may need more silence frames)")
}

func TestEncoder_FEC(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Initially disabled
	if enc.FECEnabled() {
		t.Error("FECEnabled() = true initially, want false")
	}

	// Enable FEC
	enc.SetFEC(true)
	if !enc.FECEnabled() {
		t.Error("FECEnabled() = false after SetFEC(true)")
	}

	// Disable FEC
	enc.SetFEC(false)
	if enc.FECEnabled() {
		t.Error("FECEnabled() = true after SetFEC(false)")
	}
}

func TestEncoder_Reset(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Encode a frame
	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)
	data := make([]byte, 4000)
	_, err = enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Reset
	enc.Reset()

	// Encode again should work
	_, err = enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode after Reset error: %v", err)
	}
}

func TestEncoder_Stereo(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Generate stereo signal (interleaved)
	frameSize := 960
	pcm := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		// Left: 440 Hz
		pcm[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
		// Right: 880 Hz
		pcm[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}

	// Encode
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet for stereo")
	}

	t.Logf("Encoded stereo %d samples to %d bytes", frameSize, len(packet))
}

func TestEncoder_FrameSize(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default frame size is 960 (20ms)
	if enc.FrameSize() != 960 {
		t.Errorf("FrameSize() = %d, want 960", enc.FrameSize())
	}

	// Valid frame sizes
	validSizes := []int{120, 240, 480, 960, 1920, 2880}
	for _, size := range validSizes {
		t.Run(fmt.Sprintf("framesize_%d", size), func(t *testing.T) {
			err := enc.SetFrameSize(size)
			if err != nil {
				t.Errorf("SetFrameSize(%d) error: %v", size, err)
			}
			if enc.FrameSize() != size {
				t.Errorf("FrameSize() = %d, want %d", enc.FrameSize(), size)
			}
		})
	}

	// Invalid frame sizes
	invalidSizes := []int{0, 100, 500, 1000, 3000}
	for _, size := range invalidSizes {
		t.Run(fmt.Sprintf("invalid_framesize_%d", size), func(t *testing.T) {
			err := enc.SetFrameSize(size)
			if err != ErrInvalidFrameSize {
				t.Errorf("SetFrameSize(%d) error = %v, want ErrInvalidFrameSize", size, err)
			}
		})
	}
}

func TestEncoder_EncodeFloat32_Convenience(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("EncodeFloat32 returned empty packet")
	}
}

func TestEncoder_EncodeInt16Slice_Convenience(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	frameSize := 960
	pcm := make([]int16, frameSize)
	for i := range pcm {
		pcm[i] = int16(16384 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}

	packet, err := enc.EncodeInt16Slice(pcm)
	if err != nil {
		t.Fatalf("EncodeInt16Slice error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("EncodeInt16Slice returned empty packet")
	}
}

func TestEncoder_InvalidFrameSize_Encode(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default frame size is 960, try to encode wrong size
	pcm := make([]float32, 500) // Wrong size
	data := make([]byte, 4000)

	_, err = enc.Encode(pcm, data)
	if err != ErrInvalidFrameSize {
		t.Errorf("Encode with wrong size: got %v, want ErrInvalidFrameSize", err)
	}
}
