package gopus

import (
	"fmt"
	"math"
	"testing"
)

func TestEncoder_Encode_Float32(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
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
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
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

func TestEncoder_Encode_Int24(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	frameSize := 960
	pcm := generateSineWaveInt24(48000, 440, frameSize)

	data := make([]byte, 4000)
	n, err := enc.EncodeInt24(pcm, data)
	if err != nil {
		t.Fatalf("EncodeInt24 error: %v", err)
	}

	if n == 0 {
		t.Fatal("EncodeInt24 returned 0 bytes")
	}

	packet, err := enc.EncodeInt24Slice(pcm)
	if err != nil {
		t.Fatalf("EncodeInt24Slice error: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("EncodeInt24Slice returned empty packet")
	}

	t.Logf("Encoded %d int24 samples to %d bytes", frameSize, n)
}

func TestEncoder_Encode_Int24_InvalidFrameSize(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	data := make([]byte, 4000)
	if _, err := enc.EncodeInt24(make([]int32, enc.FrameSize()-1), data); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeInt24(short) error=%v want=%v", err, ErrInvalidFrameSize)
	}
	if _, err := enc.EncodeInt24Slice(make([]int32, enc.FrameSize()-1)); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeInt24Slice(short) error=%v want=%v", err, ErrInvalidFrameSize)
	}
}

func TestEncoder_Encode_RoundTrip(t *testing.T) {
	// Create encoder and decoder
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
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
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Calculate output energy
	var outputEnergy float64
	for _, s := range pcmOut[:n*cfg.Channels] {
		outputEnergy += float64(s) * float64(s)
	}

	t.Logf("Input energy: %f, Output energy: %f", inputEnergy, outputEnergy)
	t.Logf("Encoded to %d bytes, decoded to %d samples", len(packet), n*cfg.Channels)

	// The output should have some energy (lossy compression but not zero)
	if outputEnergy == 0 {
		t.Error("Decoded output has zero energy")
	}
}

func TestEncoder_Reset(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
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
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Application: ApplicationAudio})
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
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default frame size is 960 (20ms)
	if enc.FrameSize() != 960 {
		t.Errorf("FrameSize() = %d, want 960", enc.FrameSize())
	}

	// Valid frame sizes
	validSizes := []int{120, 240, 480, 960, 1920, 2880, 3840, 4800, 5760}
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
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
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

func TestEncoder_LongPacketRoundTrip(t *testing.T) {
	cases := []struct {
		name        string
		application Application
		frameSize   int
	}{
		{name: "restricted_silk_80ms", application: ApplicationRestrictedSilk, frameSize: 3840},
		{name: "restricted_silk_100ms", application: ApplicationRestrictedSilk, frameSize: 4800},
		{name: "restricted_silk_120ms", application: ApplicationRestrictedSilk, frameSize: 5760},
		{name: "restricted_celt_80ms", application: ApplicationRestrictedCelt, frameSize: 3840},
		{name: "restricted_celt_100ms", application: ApplicationRestrictedCelt, frameSize: 4800},
		{name: "restricted_celt_120ms", application: ApplicationRestrictedCelt, frameSize: 5760},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: tc.application})
			if err != nil {
				t.Fatalf("NewEncoder error: %v", err)
			}
			if err := enc.SetFrameSize(tc.frameSize); err != nil {
				t.Fatalf("SetFrameSize(%d) error: %v", tc.frameSize, err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}

			pcmIn := generateSineWave(48000, 440, tc.frameSize)
			packet, err := enc.EncodeFloat32(pcmIn)
			if err != nil {
				t.Fatalf("EncodeFloat32 error: %v", err)
			}
			if len(packet) == 0 {
				t.Fatal("EncodeFloat32 returned empty packet")
			}

			pcmOut := make([]float32, tc.frameSize)
			n, err := dec.Decode(packet, pcmOut)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if n != tc.frameSize {
				t.Fatalf("Decode samples=%d want=%d", n, tc.frameSize)
			}

			if energy := computeEnergyFloat32(pcmOut[:n]); energy == 0 {
				t.Fatal("decoded output has zero energy")
			}
		})
	}
}

func TestEncoder_EncodeInt16Slice_Convenience(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
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
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
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

func TestEncoder_Lookahead(t *testing.T) {
	for _, tc := range lookaheadTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: tc.sampleRate, Channels: 1, Application: tc.application})
			if err != nil {
				t.Fatalf("NewEncoder error: %v", err)
			}
			if got := enc.Lookahead(); got != tc.want {
				t.Fatalf("Lookahead() = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("set_application_updates_lookahead_before_encode", func(t *testing.T) {
		enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
		if err != nil {
			t.Fatalf("NewEncoder error: %v", err)
		}
		assertLookaheadUpdatesBeforeEncode(t, enc.Lookahead, enc.SetApplication)
	})
}
