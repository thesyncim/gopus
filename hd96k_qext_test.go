//go:build gopus_qext

// These tests cover the 96 kHz API, packet framing, buffer sizes and controls.
// Native CELT packet and PCM parity have separate libopus oracle tests.
package gopus

import (
	"math"
	"testing"
)

// sin96kPCM returns a 997 Hz sine wave frame at 96 kHz, frameSize samples per
// channel.
func sin96kPCM(channels, frameSize int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		phase := 2 * math.Pi * 997 * float64(i) / 96000.0
		pcm[i*channels] = float32(0.45 * math.Sin(phase))
		if channels == 2 {
			pcm[i*channels+1] = float32(0.35 * math.Sin(phase+0.37))
		}
	}
	return pcm
}

// TestHD96kEncoderConstructorAcceptsRate verifies that NewEncoder at 96 kHz
// succeeds under gopus_qext, SampleRate() returns 96000, and FrameSize()
// returns 1920 (20ms at 96 kHz).
func TestHD96kEncoderConstructorAcceptsRate(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  96000,
		Channels:    1,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder(96000) unexpected error: %v", err)
	}
	if enc == nil {
		t.Fatal("NewEncoder(96000) returned nil")
	}
	if got := enc.SampleRate(); got != 96000 {
		t.Errorf("SampleRate() = %d, want 96000", got)
	}
	if got := enc.FrameSize(); got != 1920 {
		t.Errorf("FrameSize() = %d, want 1920 (20ms at 96 kHz)", got)
	}
}

// TestHD96kEncoderStereoConstructor verifies stereo at 96 kHz.
func TestHD96kEncoderStereoConstructor(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  96000,
		Channels:    2,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder(96000, 2) unexpected error: %v", err)
	}
	if enc.Channels() != 2 {
		t.Errorf("Channels() = %d, want 2", enc.Channels())
	}
}

// TestHD96kDecoderConstructorAcceptsRate verifies that NewDecoder at 96 kHz
// succeeds under gopus_qext, SampleRate() returns 96000.
func TestHD96kDecoderConstructorAcceptsRate(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(96000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(96000) unexpected error: %v", err)
	}
	if dec == nil {
		t.Fatal("NewDecoder(96000) returned nil")
	}
	if got := dec.SampleRate(); got != 96000 {
		t.Errorf("SampleRate() = %d, want 96000", got)
	}
	if got := dec.Channels(); got != 1 {
		t.Errorf("Channels() = %d, want 1", got)
	}
}

// TestHD96kEncoderProducesValidPackets verifies that a 96 kHz encoder produces
// non-empty packets with a CELT TOC.
func TestHD96kEncoderProducesValidPackets(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(itoaSmall(channels)+"ch", func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  96000,
				Channels:    channels,
				Application: ApplicationAudio,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			// 20ms at 96 kHz = 1920 samples
			pcm := sin96kPCM(channels, 1920)
			buf := make([]byte, 4000)
			n, err := enc.Encode(pcm, buf)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if n == 0 {
				t.Fatal("Encode returned empty packet")
			}
			pkt := buf[:n]
			// Verify it's a parseable Opus packet.
			toc := ParseTOC(pkt[0])
			if toc.Mode != ModeCELT {
				t.Errorf("expected CELT mode, got %v", toc.Mode)
			}
		})
	}
}

// TestHD96kRoundtripNonZeroAudio verifies that encoding a 96 kHz sine wave
// and decoding it at 96 kHz produces non-zero output samples.
func TestHD96kRoundtripNonZeroAudio(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run(itoaSmall(channels)+"ch", func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{
				SampleRate:  96000,
				Channels:    channels,
				Application: ApplicationAudio,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetBitrate(256000); err != nil {
				t.Fatalf("SetBitrate: %v", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(96000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			pcm96in := sin96kPCM(channels, 1920)
			buf := make([]byte, 4000)
			n, err := enc.Encode(pcm96in, buf)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			pcm96out := make([]float32, 1920*channels)
			nDecoded, err := dec.Decode(buf[:n], pcm96out)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			// Expect 1920 samples at 96 kHz (20ms).
			if nDecoded != 1920 {
				t.Errorf("Decode returned %d samples, want 1920", nDecoded)
			}
			// Verify non-zero output (sine wave encoded; should not be silence).
			nonZero := 0
			for _, s := range pcm96out[:nDecoded*channels] {
				if s != 0 {
					nonZero++
				}
			}
			if nonZero == 0 {
				t.Error("decoded 96 kHz audio is all-zero; expected non-zero for sine input")
			}
		})
	}
}

// TestHD96kDecoderLastPacketDurationIs96kSamples verifies that after decoding
// a standard CELT packet at 96 kHz, LastPacketDuration() returns the 96 kHz
// sample count (1920 for a 20ms packet).
func TestHD96kDecoderLastPacketDurationIs96kSamples(t *testing.T) {
	// Build a 48 kHz encoder to produce a standard CELT packet.
	enc48, err := NewEncoder(EncoderConfig{
		SampleRate:  48000,
		Channels:    1,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder 48k: %v", err)
	}
	pcm48 := make([]float32, 960)
	for i := range pcm48 {
		pcm48[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000.0))
	}
	buf := make([]byte, 4000)
	n, err := enc48.Encode(pcm48, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	packet := buf[:n]

	// Decode the 48 kHz packet with a 96 kHz decoder.
	dec96, err := NewDecoder(DefaultDecoderConfig(96000, 1))
	if err != nil {
		t.Fatalf("NewDecoder 96k: %v", err)
	}
	out96 := make([]float32, 1920)
	nDecoded, err := dec96.Decode(packet, out96)
	if err != nil {
		t.Fatalf("Decode 96k: %v", err)
	}
	if nDecoded != 1920 {
		t.Errorf("Decode 96k returned %d samples, want 1920 (20ms at 96 kHz)", nDecoded)
	}
	if got := dec96.LastPacketDuration(); got != 1920 {
		t.Errorf("LastPacketDuration() = %d, want 1920", got)
	}
}

// TestHD96kEncoderFrameSizeSetGet verifies SetFrameSize and FrameSize at 96 kHz.
// Frame sizes count samples per channel at 96 kHz.
func TestHD96kEncoderFrameSizeSetGet(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  96000,
		Channels:    1,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	// Default: 20ms = 1920 at 96 kHz.
	if got := enc.FrameSize(); got != 1920 {
		t.Errorf("default FrameSize() = %d, want 1920", got)
	}

	// Set to 40ms = 3840 at 96 kHz.
	if err := enc.SetFrameSize(3840); err != nil {
		t.Fatalf("SetFrameSize(3840): %v", err)
	}
	if got := enc.FrameSize(); got != 3840 {
		t.Errorf("FrameSize() after SetFrameSize(3840) = %d, want 3840", got)
	}

	// Reset back to 20ms.
	if err := enc.SetFrameSize(1920); err != nil {
		t.Fatalf("SetFrameSize(1920): %v", err)
	}
	if got := enc.FrameSize(); got != 1920 {
		t.Errorf("FrameSize() after SetFrameSize(1920) = %d, want 1920", got)
	}
}

// TestHD96kDecodeWrongBufferSizeErrors verifies ErrBufferTooSmall is returned
// when the output buffer is smaller than the 96 kHz frame size.
func TestHD96kDecodeWrongBufferSizeErrors(t *testing.T) {
	// Build a 48 kHz packet.
	enc48, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcm48 := make([]float32, 960)
	buf := make([]byte, 4000)
	n, err := enc48.Encode(pcm48, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	packet := buf[:n]

	dec96, err := NewDecoder(DefaultDecoderConfig(96000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	// A 48 kHz packet decoded at 96 kHz needs 1920 samples.
	// Providing only 960 should fail.
	small := make([]float32, 960)
	_, err = dec96.Decode(packet, small)
	if err != ErrBufferTooSmall {
		t.Errorf("Decode with too-small buffer: got %v, want ErrBufferTooSmall", err)
	}
}
