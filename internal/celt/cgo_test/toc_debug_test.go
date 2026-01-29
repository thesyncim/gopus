// Package cgo tests TOC byte interpretation.
package cgo

import (
	"testing"
)

// TestTOCByteInterpretation tests how libopus interprets the TOC byte.
func TestTOCByteInterpretation(t *testing.T) {
	t.Log("=== TOC Byte Interpretation ===")
	t.Log("")

	// TOC byte format for Opus (RFC 6716):
	// Bits 7-5: Config (determines mode: SILK, Hybrid, or CELT)
	// Bits 4-3: Frame size code
	// Bits 2:   Stereo (0=mono, 1=stereo)
	// Bits 1-0: Frame count code (0=1 frame, 1=2 frames, 2=2 frames, 3=arbitrary)

	// For CELT-only at 48kHz (config 31-32 for FB):
	// 0xF8 = 11111 000 = config=31, frameSizeCode=0, mono, 1 frame
	// 0xFC = 11111 100 = config=31, frameSizeCode=0, stereo, 1 frame

	// Config 31: CELT-only, 48kHz, 2.5ms base frame
	// Frame size codes: 0=2.5ms, 1=5ms, 2=10ms, 3=20ms

	// Let's decode TOC = 0xF8
	toc := byte(0xF8)
	config := (toc >> 3) & 0x1F // bits 7-3
	stereo := (toc >> 2) & 1    // bit 2
	frameCode := toc & 3        // bits 1-0

	t.Logf("TOC: 0x%02X = %08b", toc, toc)
	t.Logf("  Config: %d (bits 7-3)", config)
	t.Logf("  Stereo: %d (bit 2)", stereo)
	t.Logf("  Frame code: %d (bits 1-0)", frameCode)
	t.Log("")

	// According to RFC 6716 Table 2:
	// Config 24-31: CELT-only mode
	// Config 28-31: Fullband (48kHz)
	// For CELT-only, frame durations are:
	//   frameSizeCode 0 = 2.5ms (not valid for CELT FB)
	//   frameSizeCode 1 = 5ms (not valid for CELT FB)
	//   frameSizeCode 2 = 10ms = 480 samples at 48kHz
	//   frameSizeCode 3 = 20ms = 960 samples at 48kHz

	// Wait, RFC 6716 Table 2 structure:
	// Bits 7-5 encode config (0-31)
	// But actually it's:
	// Bits 7-3: Config (0-31)
	// Wait, let me re-read RFC 6716...

	// Actually from RFC 6716:
	// 0     1     2     3     4     5     6     7
	// +-----+-----+-----+-----+-----+-----+-----+-----+
	// |     config      | s   |    c    |
	// +-----+-----+-----+-----+-----+-----+-----+-----+

	// config: 5 bits (0-31)
	// s: stereo flag
	// c: frame count code (2 bits)

	// For CELT-only fullband 20ms mono:
	// config should be... let me check

	// Config values from RFC 6716 Table 2:
	// 0-3: SILK narrowband
	// 4-7: SILK mediumband
	// 8-11: SILK wideband
	// 12-13: Hybrid SWB
	// 14-15: Hybrid FB
	// 16-19: CELT narrowband
	// 20-23: CELT wideband
	// 24-27: CELT super wideband
	// 28-31: CELT fullband

	// Within each range, the low 2 bits encode frame size:
	// 0 = 2.5ms (10ms for SILK)
	// 1 = 5ms (20ms for SILK)
	// 2 = 10ms (40ms for SILK)
	// 3 = 20ms (60ms for SILK)

	// So for CELT fullband 20ms:
	// Base config = 28 + 3 (for 20ms) = 31
	// config = 31

	// TOC for CELT FB 20ms mono 1-frame:
	// config=31 (bits 7-3), stereo=0 (bit 2), c=0 (bits 1-0)
	// = 11111 0 00 = 0xF8

	t.Logf("Expected for CELT FB 20ms mono:")
	t.Logf("  config=31, stereo=0, c=0")
	t.Logf("  TOC = 0xF8")
	t.Log("")

	// Parse TOC according to RFC 6716
	t.Log("RFC 6716 TOC parsing:")

	// For config 28-31 (CELT FB), frame duration depends on low 2 bits of config:
	// config&3 = 0: 2.5ms = 120 samples
	// config&3 = 1: 5ms = 240 samples
	// config&3 = 2: 10ms = 480 samples
	// config&3 = 3: 20ms = 960 samples

	configDuration := config & 3
	var expectedSamples int
	switch configDuration {
	case 0:
		expectedSamples = 120
	case 1:
		expectedSamples = 240
	case 2:
		expectedSamples = 480
	case 3:
		expectedSamples = 960
	}
	t.Logf("  Config duration code: %d -> %d samples", configDuration, expectedSamples)

	// Frame count interpretation:
	// c=0: 1 frame
	// c=1: 2 frames, equal size
	// c=2: 2 frames, different sizes
	// c=3: arbitrary number of frames
	t.Logf("  Frame code %d = %s", frameCode, []string{"1 frame", "2 equal frames", "2 diff frames", "arbitrary"}[frameCode])

	// Total samples = expectedSamples * numFrames
	numFrames := 1
	if frameCode == 1 || frameCode == 2 {
		numFrames = 2
	}
	totalSamples := expectedSamples * numFrames
	t.Logf("  Total expected samples: %d", totalSamples)
}
