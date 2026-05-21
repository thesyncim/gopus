package gopus

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
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
			dec, err := NewDecoder(DefaultDecoderConfig(tt.sampleRate, tt.channels))
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
			dec, err := NewDecoder(DefaultDecoderConfig(rate, 1))
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
			dec, err := NewDecoder(DefaultDecoderConfig(48000, ch))
			if err != ErrInvalidChannels {
				t.Errorf("NewDecoder(48000, %d) error = %v, want ErrInvalidChannels", ch, err)
			}
			if dec != nil {
				t.Error("NewDecoder returned non-nil decoder on error")
			}
		})
	}
}

func TestNewDecoder_DefaultMaxPacketLimits(t *testing.T) {
	dec, err := NewDecoder(DecoderConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	if err != nil {
		t.Fatalf("NewDecoder() unexpected error: %v", err)
	}
	if dec.maxPacketSamples != defaultMaxPacketSamples {
		t.Fatalf("maxPacketSamples=%d want %d", dec.maxPacketSamples, defaultMaxPacketSamples)
	}
	if dec.maxPacketBytes != defaultMaxPacketBytes {
		t.Fatalf("maxPacketBytes=%d want %d", dec.maxPacketBytes, defaultMaxPacketBytes)
	}
}

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
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
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
	dec := newMonoTestDecoder(t)

	pcmOut := make([]int16, 960)
	n, err := dec.DecodeInt16(minimalHybridTestPacket20ms(), pcmOut)
	if err != nil {
		t.Fatalf("DecodeInt16 error: %v", err)
	}

	if n != 960 {
		t.Errorf("DecodeInt16 returned %d samples, want %d", n, 960)
	}
}

func TestDecodeFloatClearsSoftClipMemOnPacket(t *testing.T) {
	dec := newMonoTestDecoder(t)
	dec.softClipMem[0] = 0.25

	pcm := make([]float32, dec.maxPacketSamples*dec.channels)
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), pcm); err != nil {
		t.Fatalf("Decode packet error: %v", err)
	}
	if dec.softClipMem[0] != 0 || dec.softClipMem[1] != 0 {
		t.Fatalf("Decode packet softClipMem=%v want zeroed", dec.softClipMem)
	}
}

func TestDecodeInt16PLCPreservesSoftClipMem(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	want := [2]float32{0.25, -0.125}
	dec.softClipMem = want

	pcm := make([]int16, dec.maxPacketSamples*dec.channels)
	if _, err := dec.DecodeInt16(nil, pcm); err != nil {
		t.Fatalf("DecodeInt16 PLC error: %v", err)
	}
	if dec.softClipMem != want {
		t.Fatalf("DecodeInt16 PLC softClipMem=%v want %v", dec.softClipMem, want)
	}
}

func TestDecoder_Decode_BufferTooSmall(t *testing.T) {
	dec := newMonoTestDecoder(t)

	// Buffer too small for float32
	pcmOut := make([]float32, 480)
	_, err := dec.Decode(minimalHybridTestPacket20ms(), pcmOut)
	if err != ErrBufferTooSmall {
		t.Errorf("Decode with small buffer: got %v, want ErrBufferTooSmall", err)
	}

	// Buffer too small for int16
	pcmOut16 := make([]int16, 480)
	_, err = dec.DecodeInt16(minimalHybridTestPacket20ms(), pcmOut16)
	if err != ErrBufferTooSmall {
		t.Errorf("DecodeInt16 with small buffer: got %v, want ErrBufferTooSmall", err)
	}
}

func TestDecodeRejectsCode3DurationOver120msBeforeLocalCap(t *testing.T) {
	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketSamples = 7000
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, cfg.MaxPacketSamples)
	_, err = dec.Decode([]byte{GenerateTOC(31, false, 3), 0x07}, pcm)
	if err != ErrInvalidPacket {
		t.Fatalf("Decode error=%v want %v", err, ErrInvalidPacket)
	}
}

func TestDecodeInt16RejectsCode3DurationOver120msBeforeLocalCap(t *testing.T) {
	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketSamples = 7000
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]int16, cfg.MaxPacketSamples)
	_, err = dec.DecodeInt16([]byte{GenerateTOC(31, false, 3), 0x07}, pcm)
	if err != ErrInvalidPacket {
		t.Fatalf("DecodeInt16 error=%v want %v", err, ErrInvalidPacket)
	}
}

func TestDecodeWithFECRejectsOver120msBeforeCELTPLCFallback(t *testing.T) {
	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketSamples = 7000
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, cfg.MaxPacketSamples)
	_, err = dec.DecodeWithFEC([]byte{GenerateTOC(31, false, 3), 0x07}, pcm, true)
	if err != ErrInvalidPacket {
		t.Fatalf("DecodeWithFEC error=%v want %v", err, ErrInvalidPacket)
	}
}

func TestDecodeValid120msPacketUsesBufferSizeAfterProtocolCheck(t *testing.T) {
	dec := newMonoTestDecoder(t)
	packet := append([]byte{GenerateTOC(16, false, 3), 0x30}, make([]byte, 48)...)

	_, err := dec.Decode(packet, make([]float32, 120))
	if err != ErrBufferTooSmall {
		t.Fatalf("Decode error=%v want %v", err, ErrBufferTooSmall)
	}
}

func TestDecoder_Decode_PLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
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

func TestDecoder_Reset(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
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
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
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
			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
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
		{"CELT 2.5ms", 28, 120}, // CELT FB 2.5ms
		{"CELT 5ms", 29, 240},   // CELT FB 5ms
		{"SILK 40ms", 10, 1920}, // SILK WB 40ms
		{"SILK 60ms", 11, 2880}, // SILK WB 60ms
	}

	for _, tt := range extendedConfigs {
		t.Run(tt.name, func(t *testing.T) {
			dec, _ := NewDecoder(DefaultDecoderConfig(48000, 1))

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
	dec, _ := NewDecoder(DefaultDecoderConfig(48000, 1))

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

// TestDecodeWithFEC_FallbackToPLC verifies that DecodeWithFEC falls back to PLC
// when no FEC data is available (e.g., when no previous packet was decoded,
// or the previous packet was CELT-only mode).
func TestDecodeWithFEC_FallbackToPLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Attempt FEC decode without any previous packet - should fall back to PLC
	frameSize := 960
	pcm := make([]float32, frameSize)

	// First FEC decode with no prior data - should use PLC and produce silence/zeros
	n, err := dec.DecodeWithFEC(nil, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error (expected PLC fallback): %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("DecodeWithFEC fell back to PLC successfully, produced %d samples", n)
}

// TestDecodeWithFEC_CELTNoFEC verifies that CELT-only packets don't have FEC data.
// DecodeWithFEC should fall back to PLC after decoding a CELT packet.
func TestDecodeWithFEC_CELTNoFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Create a CELT packet (config 31 = CELT FB 20ms)
	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0) // CELT FB 20ms
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}

	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the CELT packet normally
	_, err = dec.Decode(celtPacket, pcm)
	if err != nil {
		t.Fatalf("Decode CELT packet error: %v", err)
	}

	// Check that no FEC data was stored (CELT doesn't have LBRR)
	if dec.hasFEC {
		t.Error("hasFEC should be false after CELT packet decode")
	}

	// Attempt FEC decode - should fall back to PLC since no FEC available
	n, err := dec.DecodeWithFEC(nil, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error (expected PLC fallback): %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("DecodeWithFEC correctly fell back to PLC for CELT mode")
}

// TestDecodeWithFEC_SILKNoLBRRDoesNotStoreFEC verifies that SILK packets only
// arm FEC state when their LBRR flag is present.
func TestDecodeWithFEC_SILKNoLBRRDoesNotStoreFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Create a SILK packet (config 9 = SILK WB 20ms)
	silkPacket := make([]byte, 100)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i)
	}

	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the SILK packet normally
	_, err = dec.Decode(silkPacket, pcm)
	if err != nil {
		t.Fatalf("Decode SILK packet error: %v", err)
	}

	if dec.hasFEC {
		t.Error("hasFEC should be false after SILK packet without LBRR")
	}

	t.Log("DecodeWithFEC correctly left FEC state clear for SILK without LBRR")
}

func TestStoreFECData_ReusesBackingBuffer(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	initialCap := cap(dec.fecData)
	if initialCap == 0 {
		t.Fatal("expected preallocated fecData backing buffer")
	}

	toc := TOC{
		Mode:      ModeSILK,
		Bandwidth: BandwidthWideband,
		Stereo:    false,
	}

	packetSmall := make([]byte, 32)
	packetLarge := make([]byte, 512)
	for i := range packetSmall {
		packetSmall[i] = byte(i)
	}
	for i := range packetLarge {
		packetLarge[i] = byte(255 - (i % 255))
	}
	packetSmall[0] |= 0x40
	packetLarge[0] |= 0x40

	dec.storeFECData(packetSmall, toc, 1, 960)
	if cap(dec.fecData) != initialCap {
		t.Fatalf("fecData cap changed after small packet: got %d want %d", cap(dec.fecData), initialCap)
	}

	dec.storeFECData(packetLarge, toc, 1, 960)
	if cap(dec.fecData) != initialCap {
		t.Fatalf("fecData cap changed after large packet: got %d want %d", cap(dec.fecData), initialCap)
	}
	if len(dec.fecData) != len(packetLarge) {
		t.Fatalf("fecData len = %d, want %d", len(dec.fecData), len(packetLarge))
	}
	if dec.fecData[0] != packetLarge[0] || dec.fecData[len(dec.fecData)-1] != packetLarge[len(packetLarge)-1] {
		t.Fatal("fecData content mismatch after copy")
	}
}

func TestStoreFECData_NoLBRRClearsState(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK

	toc := TOC{
		Mode:      ModeSILK,
		Bandwidth: BandwidthWideband,
		FrameSize: 960,
	}
	dec.storeFECData([]byte{0x00, 0x01}, toc, 1, 960)
	if dec.hasFEC {
		t.Fatal("storeFECData kept FEC armed for packet without LBRR")
	}
}

func TestDecodeFECFrame_BufferSizingUsesSingleFrame(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Simulate stored FEC metadata from a 2-frame packet. decode_fec should still
	// only require one frame of output buffer.
	dec.hasFEC = true
	dec.fecData = []byte{0x01, 0x02, 0x03, 0x04}
	dec.fecMode = ModeSILK
	dec.fecBandwidth = BandwidthWideband
	dec.fecStereo = false
	dec.fecFrameSize = 960
	dec.fecFrameCount = 2

	pcm := make([]float32, 960)
	_, err = dec.decodeFECFrame(pcm)
	if err == ErrBufferTooSmall {
		t.Fatal("decodeFECFrame rejected single-frame output buffer for multi-frame packet metadata")
	}
}

// TestDecodeWithFEC_HybridLBRRNormalDecodeDoesNotCacheFEC verifies that normal
// decode skips LBRR instead of caching it for a later nil decode_fec call.
func TestDecodeWithFEC_HybridLBRRNormalDecodeDoesNotCacheFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Use the minimal hybrid test packet
	packet := minimalHybridTestPacket20ms()
	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the Hybrid packet normally
	_, err = dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode Hybrid packet error: %v", err)
	}

	if dec.hasFEC {
		t.Error("hasFEC should be false after normal Hybrid decode")
	}
}

// TestDecodeWithFEC_Recovery tests FEC recovery flow.
// This test simulates packet loss and FEC recovery.
func TestDecodeWithFEC_Recovery(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Decode a series of SILK packets to build up state
	silkPacket := make([]byte, 100)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i * 3)
	}

	frameSize := 960
	pcm1 := make([]float32, frameSize)
	pcm2 := make([]float32, frameSize)

	// Decode packet 1
	_, err = dec.Decode(silkPacket, pcm1)
	if err != nil {
		t.Fatalf("Decode packet 1 error: %v", err)
	}

	// Simulate packet 2 is lost - use FEC to recover
	// In real usage, we'd use the NEXT packet's LBRR to recover the lost one.
	// For this test, we just verify DecodeWithFEC works without crashing.
	n, err := dec.DecodeWithFEC(nil, pcm2, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error: %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("FEC recovery produced %d samples", n)
}

func TestDecodeWithFEC_UsesProvidedPacketAndPreservesNormalDecode(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	frameSize := 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*440*tm) + 0.2*math.Sin(2*math.Pi*880*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating FEC test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost" in decode sequence
	packet2 := encodeFrame(makeFrame(1920))

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}

	// Recover the missing packet using packet2's LBRR.
	pcmFEC := make([]float32, frameSize)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nFEC, frameSize)
	}

	// The same packet must still decode normally after FEC recovery.
	pcm2 := make([]float32, frameSize)
	n2, err := dec.Decode(packet2, pcm2)
	if err != nil {
		t.Fatalf("Decode packet2 after FEC recovery error: %v", err)
	}
	if n2 != frameSize {
		t.Fatalf("Decode packet2 after FEC recovery samples=%d want=%d", n2, frameSize)
	}
}

func TestDecodeWithFEC_FrameSizeTransitionUsesProvidedPacketGranularity(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	makeFrame := func(frameSize int, phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*330*tm) + 0.2*math.Sin(2*math.Pi*660*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(frameSize int, phase float64) []byte {
		if err := enc.SetFrameSize(frameSize); err != nil {
			t.Fatalf("SetFrameSize(%d) error: %v", frameSize, err)
		}
		n, err := enc.Encode(makeFrame(frameSize, phase), pktBuf)
		if err != nil {
			t.Fatalf("Encode(%d) error: %v", frameSize, err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating frame-size transition packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(1920, 0)
	_ = encodeFrame(960, 1920) // packet 1 intentionally "lost"
	packet2 := encodeFrame(960, 2880)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, 1920)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}

	// Recover the missing 20ms packet from packet2 LBRR while previous decode
	// state still reflects a 40ms frame.
	pcmFEC := make([]float32, 1920)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != 960 {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=960", nFEC)
	}

	pcm2 := make([]float32, 960)
	n2, err := dec.Decode(packet2, pcm2)
	if err != nil {
		t.Fatalf("Decode packet2 after FEC recovery error: %v", err)
	}
	if n2 != 960 {
		t.Fatalf("Decode packet2 after FEC recovery samples=%d want=960", n2)
	}
}

func TestDecodeWithFEC_ProvidedCELTPacketFallsBackToPLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Seed last frame size/state with a normal decode.
	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0) // CELT FB 20ms
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}

	pcm := make([]float32, 960)
	if _, err := dec.Decode(celtPacket, pcm); err != nil {
		t.Fatalf("Decode CELT packet error: %v", err)
	}

	// CELT has no LBRR, so this should transparently fall back to PLC.
	n, err := dec.DecodeWithFEC(celtPacket, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) error: %v", err)
	}
	if n != 960 {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) samples=%d want=%d", n, 960)
	}
}

func TestDecodeWithFEC_ProvidedCELTPacketClearsStoredFECState(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK
	dec.fecFrameSize = 960
	pcm := make([]float32, 960)

	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0)
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}
	if _, err := dec.DecodeWithFEC(celtPacket, pcm, true); err != nil {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) error: %v", err)
	}
	if dec.hasFEC {
		t.Fatalf("hasFEC should be false after CELT-based decode_fec fallback")
	}
}

func TestDecodeWithFEC_ProvidedPacketUsesPacketModeForCELTGate(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	frameSize := 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*440*tm) + 0.2*math.Sin(2*math.Pi*880*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating FEC test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost"
	packet2 := encodeFrame(makeFrame(1920))
	toc, _, err := packetFrameCount(packet2)
	if err != nil {
		t.Fatalf("packetFrameCount(packet2) error: %v", err)
	}
	if toc.Mode == ModeCELT {
		t.Fatalf("test setup invalid: packet2 mode resolved to CELT")
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}
	if dec.lastPacketMode == ModeCELT {
		t.Fatalf("test setup invalid: first packet mode resolved to CELT")
	}

	// Simulate transient prevMode=CELLT (e.g., after redundancy/PLC path).
	// decode_fec gating should still use packet-mode state (lastPacketMode).
	dec.prevMode = ModeCELT

	pcmFEC := make([]float32, frameSize)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nFEC, frameSize)
	}
	if dec.prevMode != toc.Mode {
		t.Fatalf("prevMode=%v want %v (provided packet mode must override transient PLC mode)", dec.prevMode, toc.Mode)
	}
}

func TestDecodeWithFEC_ProvidedPacketWithoutLBRRUsesDirectPLCFallback(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(false)
	if err := enc.SetBitrate(12000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}

	const frameSize = 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.4*math.Sin(2*math.Pi*220*tm) + 0.1*math.Sin(2*math.Pi*440*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating no-LBRR test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost"
	packet2 := encodeFrame(makeFrame(1920))

	toc, _, err := packetFrameCount(packet2)
	if err != nil {
		t.Fatalf("packetFrameCount(packet2) error: %v", err)
	}
	if toc.Mode == ModeCELT {
		t.Fatalf("test setup invalid: packet2 mode resolved to CELT")
	}
	firstFrameData, err := extractFirstFramePayload(packet2, toc)
	if err != nil {
		t.Fatalf("extractFirstFramePayload(packet2) error: %v", err)
	}
	if packetHasLBRR(firstFrameData, toc) {
		t.Fatalf("test setup invalid: packet2 unexpectedly has LBRR")
	}

	decExpected, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(expected) error: %v", err)
	}
	decActual, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(actual) error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := decExpected.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 (expected) error: %v", err)
	}
	if _, err := decActual.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 (actual) error: %v", err)
	}

	pcmExpected := make([]float32, frameSize)
	nExpected, err := decExpected.decodePLCForFECWithState(pcmExpected, frameSize, toc.Mode, toc.Bandwidth, toc.Stereo)
	if err != nil {
		t.Fatalf("decodePLCForFECWithState(expected) error: %v", err)
	}
	if nExpected != frameSize {
		t.Fatalf("decodePLCForFECWithState(expected) samples=%d want=%d", nExpected, frameSize)
	}

	pcmActual := make([]float32, frameSize)
	nActual, err := decActual.DecodeWithFEC(packet2, pcmActual, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nActual != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nActual, frameSize)
	}

	const tol = 1e-7
	for i := 0; i < frameSize; i++ {
		if math.Abs(float64(pcmExpected[i]-pcmActual[i])) > tol {
			t.Fatalf("sample %d mismatch: expected=%v actual=%v", i, pcmExpected[i], pcmActual[i])
		}
	}
}

func TestExtractFirstFramePayloadCode3VBROneFrameWithPadding(t *testing.T) {
	packet := append([]byte{0x43, 0xC1, 0x03}, []byte{0x4D, 0x66, 0xDD, 0x53, 0xE3}...)
	packet = append(packet, 0x00, 0x00, 0x00)

	firstFrameData, err := extractFirstFramePayload(packet, ParseTOC(packet[0]))
	if err != nil {
		t.Fatalf("extractFirstFramePayload error: %v", err)
	}

	want := []byte{0x4D, 0x66, 0xDD, 0x53, 0xE3}
	if len(firstFrameData) != len(want) {
		t.Fatalf("payload length=%d want=%d", len(firstFrameData), len(want))
	}
	for i := range want {
		if firstFrameData[i] != want[i] {
			t.Fatalf("payload[%d]=0x%02x want=0x%02x", i, firstFrameData[i], want[i])
		}
	}
}

func TestDecodeCode3VBROneFramePaddingRegression(t *testing.T) {
	packet := mustDecodeHex(t, "43c1064d66dd53e3b92d85ca64ec672fb6384f7b2dd2cb3164f5e17ae7b97e7a7e69544afe2e8880")

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, cfg.MaxPacketSamples)
	n, err := dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n <= 0 || n > cfg.MaxPacketSamples {
		t.Fatalf("Decode samples=%d outside (0,%d]", n, cfg.MaxPacketSamples)
	}
}

func TestDecodeWithFEC_PLCWithProvidedStateUsesProvidedMode(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960
	packet := minimalHybridTestPacket20ms()
	pcmPrime := make([]float32, frameSize)
	if _, err := dec.Decode(packet, pcmPrime); err != nil {
		t.Fatalf("Decode prime packet error: %v", err)
	}

	// Force decoder transient PLC mode to CELT and verify provided state wins.
	dec.prevMode = ModeCELT
	dec.prevRedundancy = false
	dec.haveDecoded = true

	pcmPLC := make([]float32, frameSize)
	n, err := dec.decodePLCForFECWithState(pcmPLC, frameSize, ModeHybrid, BandwidthFullband, false)
	if err != nil {
		t.Fatalf("decodePLCForFECWithState error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("decodePLCForFECWithState samples=%d want=%d", n, frameSize)
	}
	if dec.prevMode != ModeHybrid {
		t.Fatalf("prevMode=%v want=%v (provided PLC mode must be honored)", dec.prevMode, ModeHybrid)
	}
}

// TestDecodeWithFEC_NoFECRequested verifies that when fec=false, DecodeWithFEC
// behaves identically to Decode.
func TestDecodeWithFEC_NoFECRequested(t *testing.T) {
	dec1, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	dec2, _ := NewDecoder(DefaultDecoderConfig(48000, 1))

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	pcm1 := make([]float32, frameSize)
	pcm2 := make([]float32, frameSize)

	// Decode with Decode
	n1, err1 := dec1.Decode(packet, pcm1)
	// Decode with DecodeWithFEC(fec=false)
	n2, err2 := dec2.DecodeWithFEC(packet, pcm2, false)

	if err1 != err2 {
		t.Errorf("Errors differ: Decode=%v, DecodeWithFEC=%v", err1, err2)
	}
	if n1 != n2 {
		t.Errorf("Sample counts differ: Decode=%d, DecodeWithFEC=%d", n1, n2)
	}

	// Verify samples are identical
	for i := 0; i < n1*1; i++ {
		if pcm1[i] != pcm2[i] {
			t.Errorf("Sample %d differs: Decode=%v, DecodeWithFEC=%v", i, pcm1[i], pcm2[i])
			break
		}
	}

	t.Log("DecodeWithFEC(fec=false) behaves identically to Decode")
}

// TestDecodeWithFEC_ResetClearsFEC verifies that Reset clears FEC state.
func TestDecodeWithFEC_ResetClearsFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK
	dec.fecFrameSize = 960

	// Reset the decoder
	dec.Reset()

	// FEC state should be cleared
	if dec.hasFEC {
		t.Error("hasFEC should be false after Reset")
	}
	if dec.fecFrameSize != 0 {
		t.Error("fecFrameSize should be 0 after Reset")
	}

	t.Log("Reset correctly clears FEC state")
}

func TestDecoder_BandwidthAndLastPacketDuration(t *testing.T) {
	dec := newMonoTestDecoder(t)
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() before decode=%d want=0", got)
	}
	n := decodeMinimalHybrid20ms(t, dec)
	if n != 960 {
		t.Fatalf("Decode returned %d samples, want 960", n)
	}

	if got := dec.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth()=%v want=%v", got, BandwidthFullband)
	}
	if got := dec.LastPacketDuration(); got != 960 {
		t.Fatalf("LastPacketDuration()=%d want=960", got)
	}
	dec.Reset()
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() after Reset=%d want=0", got)
	}
}

func TestDecoder_InDTX(t *testing.T) {
	dec := newMonoTestDecoder(t)

	if dec.InDTX() {
		t.Fatal("InDTX()=true before any packet")
	}

	dec.lastDataLen = 2
	if !dec.InDTX() {
		t.Fatal("InDTX()=false for 2-byte packet length")
	}

	dec.lastDataLen = 3
	if dec.InDTX() {
		t.Fatal("InDTX()=true for 3-byte packet length")
	}
}

func TestDecoder_SetGainBounds(t *testing.T) {
	dec := newMonoTestDecoder(t)

	if err := dec.SetGain(-32769); err != ErrInvalidGain {
		t.Fatalf("SetGain(-32769) error=%v want=%v", err, ErrInvalidGain)
	}
	if err := dec.SetGain(32768); err != ErrInvalidGain {
		t.Fatalf("SetGain(32768) error=%v want=%v", err, ErrInvalidGain)
	}

	for _, gain := range []int{-32768, 0, 256, 32767} {
		if err := dec.SetGain(gain); err != nil {
			t.Fatalf("SetGain(%d) unexpected error: %v", gain, err)
		}
		if got := dec.Gain(); got != gain {
			t.Fatalf("Gain()=%d want=%d", got, gain)
		}
	}
}

func TestDecoder_ComplexityControl(t *testing.T) {
	dec := newMonoTestDecoder(t)
	if got := dec.Complexity(); got != 0 {
		t.Fatalf("Complexity() default=%d want 0", got)
	}

	if err := dec.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity(7) error: %v", err)
	}
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("Complexity()=%d want 7", got)
	}
	if got := dec.celtDecoder.Complexity(); got != 7 {
		t.Fatalf("CELT Complexity()=%d want 7", got)
	}
	if got := dec.hybridDecoder.Complexity(); got != 7 {
		t.Fatalf("Hybrid Complexity()=%d want 7", got)
	}

	if err := dec.SetComplexity(11); err != ErrInvalidComplexity {
		t.Fatalf("SetComplexity(11) error=%v want %v", err, ErrInvalidComplexity)
	}
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("invalid SetComplexity changed setting to %d", got)
	}

	dec.Reset()
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("Reset changed Complexity() to %d", got)
	}
}

func TestDecoder_PhaseInversionControl(t *testing.T) {
	mono := newMonoTestDecoder(t)
	if !mono.PhaseInversionDisabled() {
		t.Fatal("mono PhaseInversionDisabled()=false want true")
	}
	mono.SetPhaseInversionDisabled(false)
	if mono.PhaseInversionDisabled() {
		t.Fatal("mono PhaseInversionDisabled()=true after Set(false)")
	}
	mono.Reset()
	if mono.PhaseInversionDisabled() {
		t.Fatal("mono Reset changed phase inversion control")
	}

	stereo, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder stereo error: %v", err)
	}
	if stereo.PhaseInversionDisabled() {
		t.Fatal("stereo PhaseInversionDisabled()=true want false")
	}
	stereo.SetPhaseInversionDisabled(true)
	if !stereo.PhaseInversionDisabled() {
		t.Fatal("stereo PhaseInversionDisabled()=false after Set(true)")
	}
	stereo.Reset()
	if !stereo.PhaseInversionDisabled() {
		t.Fatal("stereo Reset changed phase inversion control")
	}
}

func TestDecoder_IgnoreExtensions(t *testing.T) {
	assertIgnoreExtensionsControls(t, newMonoTestDecoder(t))
}

func TestDecoder_OptionalExtensionControls(t *testing.T) {
	dec := newMonoTestDecoder(t)

	assertOptionalDecoderControls(t, dec)
	if osce, ok := any(dec).(extraOSCEBWEControl); ok {
		if extsupport.OSCEBWERuntime {
			assertWorkingOSCEBWEControl(t, osce)
		} else {
			t.Fatal("non-OSCE-runtime build unexpectedly exposes OSCE BWE control")
		}
	} else if extsupport.OSCEBWERuntime {
		t.Fatal("OSCE runtime build does not expose OSCE BWE control")
	}
}

func TestDecoder_GainAppliedToDecodeOutput(t *testing.T) {
	packet := minimalHybridTestPacket20ms()

	base, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder base error: %v", err)
	}
	withGain, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder withGain error: %v", err)
	}
	if err := withGain.SetGain(256); err != nil {
		t.Fatalf("SetGain(+1dB) error: %v", err)
	}

	pcmBase := make([]float32, 960)
	pcmGain := make([]float32, 960)

	nBase, err := base.Decode(packet, pcmBase)
	if err != nil {
		t.Fatalf("base Decode error: %v", err)
	}
	nGain, err := withGain.Decode(packet, pcmGain)
	if err != nil {
		t.Fatalf("withGain Decode error: %v", err)
	}
	if nBase != nGain {
		t.Fatalf("decode sample mismatch: base=%d gain=%d", nBase, nGain)
	}

	rms := func(x []float32) float64 {
		if len(x) == 0 {
			return 0
		}
		var sum float64
		for _, v := range x {
			f := float64(v)
			sum += f * f
		}
		return math.Sqrt(sum / float64(len(x)))
	}

	baseRMS := rms(pcmBase[:nBase])
	gainRMS := rms(pcmGain[:nGain])
	if baseRMS == 0 || gainRMS == 0 {
		t.Fatalf("unexpected zero RMS: base=%.8f gain=%.8f", baseRMS, gainRMS)
	}

	gotRatio := gainRMS / baseRMS
	wantRatio := float64(decodeGainLinear(256))
	if math.Abs(gotRatio-wantRatio) > 0.02 {
		t.Fatalf("gain RMS ratio=%.6f want≈%.6f (tol=0.02)", gotRatio, wantRatio)
	}
}

func TestDecoder_PitchGetter(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	if got := dec.Pitch(); got != 0 {
		t.Fatalf("Pitch()=%d want=0 before decode", got)
	}

	celtEnc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder CELT error: %v", err)
	}
	if err := celtEnc.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode(CELT) error: %v", err)
	}
	packet := make([]byte, 4000)
	n, err := celtEnc.Encode(generateSineWave(48000, 440, 960), packet)
	if err != nil {
		t.Fatalf("CELT Encode error: %v", err)
	}
	pcm := make([]float32, 960)
	if _, err := dec.Decode(packet[:n], pcm); err != nil {
		t.Fatalf("CELT Decode error: %v", err)
	}

	got := dec.Pitch()
	want := dec.celtDecoder.PostfilterPeriod()
	if got != want {
		t.Fatalf("CELT Pitch()=%d want=%d", got, want)
	}
	if got < 0 {
		t.Fatalf("Pitch() should not be negative: %d", got)
	}

	dec.Reset()
	silkEnc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder SILK error: %v", err)
	}
	if err := silkEnc.SetMode(EncoderModeSILK); err != nil {
		t.Fatalf("SetMode(SILK) error: %v", err)
	}
	if err := silkEnc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth(WB) error: %v", err)
	}
	if err := silkEnc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal(Voice) error: %v", err)
	}
	n, err = silkEnc.Encode(generateSineWave(48000, 200, 960), packet)
	if err != nil {
		t.Fatalf("SILK Encode error: %v", err)
	}
	if _, err := dec.Decode(packet[:n], pcm); err != nil {
		t.Fatalf("SILK Decode error: %v", err)
	}
	want = 0
	if dec.silkDecoder.GetLastSignalType() == 2 {
		want = dec.silkDecoder.GetLagPrev() * 3
		if want <= 0 {
			t.Fatalf("SILK lagPrev=%d produced invalid pitch", dec.silkDecoder.GetLagPrev())
		}
	}
	if got := dec.Pitch(); got != want {
		t.Fatalf("SILK Pitch()=%d want=%d", got, want)
	}

	for _, tc := range []struct {
		bandwidth Bandwidth
		want      int
	}{
		{BandwidthNarrowband, 6},
		{BandwidthMediumband, 4},
		{BandwidthWideband, 3},
		{BandwidthSuperwideband, 3},
		{BandwidthFullband, 3},
	} {
		if got := silkPitchScale(tc.bandwidth); got != tc.want {
			t.Fatalf("silkPitchScale(%v)=%d want=%d", tc.bandwidth, got, tc.want)
		}
	}
}
