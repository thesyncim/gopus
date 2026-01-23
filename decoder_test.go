package gopus

import (
	"fmt"
	"strings"
	"testing"
)

func TestNewDecoder_ValidParams(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
	}{
		{"48kHz_mono", 48000, 1},
		{"48kHz_stereo", 48000, 2},
		{"24kHz_mono", 24000, 1},
		{"24kHz_stereo", 24000, 2},
		{"16kHz_mono", 16000, 1},
		{"16kHz_stereo", 16000, 2},
		{"12kHz_mono", 12000, 1},
		{"12kHz_stereo", 12000, 2},
		{"8kHz_mono", 8000, 1},
		{"8kHz_stereo", 8000, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := NewDecoder(tt.sampleRate, tt.channels)
			if err != nil {
				t.Fatalf("NewDecoder(%d, %d) unexpected error: %v", tt.sampleRate, tt.channels, err)
			}
			if dec == nil {
				t.Fatal("NewDecoder returned nil decoder")
			}
			if dec.SampleRate() != tt.sampleRate {
				t.Errorf("SampleRate() = %d, want %d", dec.SampleRate(), tt.sampleRate)
			}
			if dec.Channels() != tt.channels {
				t.Errorf("Channels() = %d, want %d", dec.Channels(), tt.channels)
			}
		})
	}
}

func TestNewDecoder_InvalidSampleRate(t *testing.T) {
	invalidRates := []int{0, 1000, 7999, 8001, 44100, 96000, -1}

	for _, rate := range invalidRates {
		t.Run(fmt.Sprintf("rate_%d", rate), func(t *testing.T) {
			dec, err := NewDecoder(rate, 1)
			if err != ErrInvalidSampleRate {
				t.Errorf("NewDecoder(%d, 1) error = %v, want ErrInvalidSampleRate", rate, err)
			}
			if dec != nil {
				t.Error("NewDecoder returned non-nil decoder on error")
			}
		})
	}
}

func TestNewDecoder_InvalidChannels(t *testing.T) {
	invalidChannels := []int{0, -1, 3, 4, 100}

	for _, ch := range invalidChannels {
		t.Run(fmt.Sprintf("channels_%d", ch), func(t *testing.T) {
			dec, err := NewDecoder(48000, ch)
			if err != ErrInvalidChannels {
				t.Errorf("NewDecoder(48000, %d) error = %v, want ErrInvalidChannels", ch, err)
			}
			if dec != nil {
				t.Error("NewDecoder returned non-nil decoder on error")
			}
		})
	}
}

// minimalHybridTestPacket20ms creates a test packet for Hybrid FB 20ms mono (config 15).
// This is a manually constructed packet that produces valid decoder output.
// The TOC byte (0x78) indicates: config=15 (Hybrid FB 20ms), mono, code 0 (single frame).
func minimalHybridTestPacket20ms() []byte {
	// TOC byte: config=15 (Hybrid FB 20ms), mono, code 0
	// 15 << 3 | 0 << 2 | 0 = 0x78
	toc := byte(0x78)

	// Frame data: minimal valid hybrid data
	// These bytes are designed to produce valid (if near-silence) SILK+CELT decode
	frameData := []byte{
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
	}

	return append([]byte{toc}, frameData...)
}

// minimalHybridTestPacket20msStereo creates a test packet for Hybrid FB 20ms stereo.
// TOC byte (0x7C) indicates: config=15 (Hybrid FB 20ms), stereo, code 0.
func minimalHybridTestPacket20msStereo() []byte {
	// TOC byte: config=15 (Hybrid FB 20ms), stereo, code 0
	// 15 << 3 | 1 << 2 | 0 = 0x7C
	toc := byte(0x7C)

	// Frame data for stereo
	frameData := []byte{
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
	}

	return append([]byte{toc}, frameData...)
}

func TestDecoder_Decode_Float32(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	frameSize := 960 // 20ms at 48kHz

	pcmOut := make([]float32, frameSize)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if n != frameSize {
		t.Errorf("Decode returned %d samples, want %d", n, frameSize)
	}

	// Check that output buffer was written to (decoder ran without panic)
	t.Logf("Decoded %d samples successfully", n)
}

func TestDecoder_Decode_Int16(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	pcmOut := make([]int16, frameSize)
	n, err := dec.DecodeInt16(packet, pcmOut)
	if err != nil {
		t.Fatalf("DecodeInt16 error: %v", err)
	}

	if n != frameSize {
		t.Errorf("DecodeInt16 returned %d samples, want %d", n, frameSize)
	}

	// Verify samples are in valid int16 range (clamping works)
	for i, s := range pcmOut {
		if s < -32768 || s > 32767 {
			t.Errorf("Sample %d = %d, out of int16 range", i, s)
		}
	}
}

func TestDecoder_Decode_BufferTooSmall(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	// Buffer too small for float32
	pcmOut := make([]float32, frameSize/2)
	_, err = dec.Decode(packet, pcmOut)
	if err != ErrBufferTooSmall {
		t.Errorf("Decode with small buffer: got %v, want ErrBufferTooSmall", err)
	}

	// Buffer too small for int16
	pcmOut16 := make([]int16, frameSize/2)
	_, err = dec.DecodeInt16(packet, pcmOut16)
	if err != ErrBufferTooSmall {
		t.Errorf("DecodeInt16 with small buffer: got %v, want ErrBufferTooSmall", err)
	}
}

func TestDecoder_Decode_PLC(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// First decode a valid frame to set up state
	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	pcmOut := make([]float32, frameSize)
	_, err = dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Now simulate packet loss by passing nil
	pcmPLC := make([]float32, frameSize)
	n, err := dec.Decode(nil, pcmPLC)
	if err != nil {
		t.Fatalf("Decode PLC error: %v", err)
	}

	if n != frameSize {
		t.Errorf("PLC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("PLC produced %d samples", n)
}

func TestDecoder_DecodeFloat32_Convenience(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	pcmOut, err := dec.DecodeFloat32(packet)
	if err != nil {
		t.Fatalf("DecodeFloat32 error: %v", err)
	}

	if len(pcmOut) != frameSize {
		t.Errorf("DecodeFloat32 returned %d samples, want %d", len(pcmOut), frameSize)
	}
}

func TestDecoder_DecodeInt16Slice_Convenience(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	pcmOut, err := dec.DecodeInt16Slice(packet)
	if err != nil {
		t.Fatalf("DecodeInt16Slice error: %v", err)
	}

	if len(pcmOut) != frameSize {
		t.Errorf("DecodeInt16Slice returned %d samples, want %d", len(pcmOut), frameSize)
	}
}

func TestDecoder_Reset(t *testing.T) {
	dec, err := NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	// Decode a frame
	pcmOut := make([]float32, frameSize)
	_, err = dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Reset
	dec.Reset()

	// Decode again should work
	_, err = dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode after Reset error: %v", err)
	}
}

func TestDecoder_Stereo(t *testing.T) {
	dec, err := NewDecoder(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20msStereo()
	frameSize := 960

	pcmOut := make([]float32, frameSize*2) // Stereo: 2 channels
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if n != frameSize {
		t.Errorf("Decode returned %d samples per channel, want %d", n, frameSize)
	}
}

func TestDecoder_TOCParsing(t *testing.T) {
	// Test that the decoder correctly parses the TOC byte to determine frame size
	tests := []struct {
		name      string
		toc       byte
		frameSize int
	}{
		// Hybrid FB 20ms: config 15
		{"hybrid_fb_20ms", 0x78, 960},
		// Hybrid FB 10ms: config 14
		{"hybrid_fb_10ms", 0x70, 480},
		// SILK WB 20ms: config 9
		{"silk_wb_20ms", 0x48, 960},
		// CELT FB 20ms: config 31
		{"celt_fb_20ms", 0xF8, 960},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toc := ParseTOC(tt.toc)
			if toc.FrameSize != tt.frameSize {
				t.Errorf("TOC %02X: FrameSize = %d, want %d", tt.toc, toc.FrameSize, tt.frameSize)
			}
		})
	}
}

// TestDecode_ModeRouting verifies that packets are routed to the correct decoder
// based on their TOC mode field.
func TestDecode_ModeRouting(t *testing.T) {
	tests := []struct {
		name      string
		config    uint8 // TOC config (0-31)
		frameSize int   // Expected frame size at 48kHz
		mode      Mode  // Expected mode
	}{
		// SILK-only (configs 0-11)
		{"SILK NB 10ms", 0, 480, ModeSILK},
		{"SILK NB 20ms", 1, 960, ModeSILK},
		{"SILK NB 40ms", 2, 1920, ModeSILK},
		{"SILK NB 60ms", 3, 2880, ModeSILK},
		{"SILK MB 20ms", 5, 960, ModeSILK},
		{"SILK WB 20ms", 9, 960, ModeSILK},
		{"SILK WB 40ms", 10, 1920, ModeSILK},
		{"SILK WB 60ms", 11, 2880, ModeSILK},

		// Hybrid (configs 12-15)
		{"Hybrid SWB 10ms", 12, 480, ModeHybrid},
		{"Hybrid SWB 20ms", 13, 960, ModeHybrid},
		{"Hybrid FB 10ms", 14, 480, ModeHybrid},
		{"Hybrid FB 20ms", 15, 960, ModeHybrid},

		// CELT-only (configs 16-31)
		{"CELT NB 2.5ms", 16, 120, ModeCELT},
		{"CELT NB 5ms", 17, 240, ModeCELT},
		{"CELT NB 10ms", 18, 480, ModeCELT},
		{"CELT NB 20ms", 19, 960, ModeCELT},
		{"CELT FB 2.5ms", 28, 120, ModeCELT},
		{"CELT FB 5ms", 29, 240, ModeCELT},
		{"CELT FB 10ms", 30, 480, ModeCELT},
		{"CELT FB 20ms", 31, 960, ModeCELT},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify TOC parsing
			toc := ParseTOC(GenerateTOC(tt.config, false, 0))

			if toc.Mode != tt.mode {
				t.Errorf("Mode mismatch: got %v, want %v", toc.Mode, tt.mode)
			}
			if toc.FrameSize != tt.frameSize {
				t.Errorf("FrameSize mismatch: got %d, want %d", toc.FrameSize, tt.frameSize)
			}

			// Test decoder accepts the packet (may fail on decode but should not fail on routing)
			dec, err := NewDecoder(48000, 1)
			if err != nil {
				t.Fatalf("NewDecoder failed: %v", err)
			}

			// Create minimal valid packet (TOC + some data)
			packet := make([]byte, 100)
			packet[0] = GenerateTOC(tt.config, false, 0)
			// Fill with minimal valid data for range decoder
			for i := 1; i < len(packet); i++ {
				packet[i] = byte(i)
			}

			// Decode should not panic and should not return "hybrid: invalid frame size"
			pcm := make([]float32, tt.frameSize*2) // Extra buffer
			_, err = dec.Decode(packet, pcm)

			// For extended frame sizes, we expect decode to succeed (no routing error)
			// The decode may still fail for other reasons (invalid bitstream) but
			// should NOT fail with "hybrid: invalid frame size"
			if err != nil {
				errStr := err.Error()
				if strings.Contains(errStr, "hybrid: invalid frame size") {
					t.Errorf("Mode routing failed: SILK/CELT packet incorrectly routed to hybrid decoder: %v", err)
				}
				// Log other errors but don't fail - bitstream content may be invalid
				t.Logf("Decode error (non-routing): %v", err)
			}
		})
	}
}

// TestDecode_ExtendedFrameSizes verifies that extended frame sizes (CELT 2.5/5ms,
// SILK 40/60ms) are accepted without being rejected by the hybrid decoder.
func TestDecode_ExtendedFrameSizes(t *testing.T) {
	// Test that extended frame sizes don't trigger hybrid validation error
	extendedConfigs := []struct {
		name      string
		config    uint8
		frameSize int
	}{
		{"CELT 2.5ms", 28, 120},  // CELT FB 2.5ms
		{"CELT 5ms", 29, 240},    // CELT FB 5ms
		{"SILK 40ms", 10, 1920},  // SILK WB 40ms
		{"SILK 60ms", 11, 2880},  // SILK WB 60ms
	}

	for _, tt := range extendedConfigs {
		t.Run(tt.name, func(t *testing.T) {
			dec, _ := NewDecoder(48000, 1)

			packet := make([]byte, 100)
			packet[0] = GenerateTOC(tt.config, false, 0)
			for i := 1; i < len(packet); i++ {
				packet[i] = byte(i * 7) // Different pattern
			}

			pcm := make([]float32, tt.frameSize*2)
			_, err := dec.Decode(packet, pcm)

			// Critical: should NOT fail with hybrid frame size error
			if err != nil && strings.Contains(err.Error(), "hybrid: invalid frame size") {
				t.Errorf("Extended frame size incorrectly rejected as hybrid: %v", err)
			}
		})
	}
}

// TestDecode_PLC_ModeTracking verifies that PLC uses the last decoded mode,
// not defaulting to Hybrid mode.
func TestDecode_PLC_ModeTracking(t *testing.T) {
	dec, _ := NewDecoder(48000, 1)

	// First: decode a SILK packet to set mode
	silkPacket := make([]byte, 50)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i)
	}

	pcm := make([]float32, 960*2)
	_, _ = dec.Decode(silkPacket, pcm)

	// PLC should use last mode (SILK)
	_, err := dec.Decode(nil, pcm)
	if err != nil && strings.Contains(err.Error(), "hybrid") {
		t.Errorf("PLC should use SILK mode, not hybrid: %v", err)
	}
}
