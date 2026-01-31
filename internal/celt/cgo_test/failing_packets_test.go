// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestFailingLibopusPackets investigates packets where libopus returns 0 samples.
func TestFailingLibopusPackets(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector01.bit"

	packets, err := loadPacketsSimple(bitFile, 25)
	if err != nil {
		t.Skip("Could not load packets:", err)
	}

	toc := gopus.ParseTOC(packets[0][0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}

	libDec, _ := NewLibopusDecoder(48000, channels)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	for i, pkt := range packets {
		pktTOC := gopus.ParseTOC(pkt[0])

		// Decode with libopus
		_, libN := libDec.DecodeFloat(pkt, pktTOC.FrameSize*4)

		if libN <= 0 {
			// Failing packet - investigate
			tocByte := pkt[0]
			config := tocByte >> 3
			stereo := (tocByte & 0x04) != 0
			frameCode := tocByte & 0x03

			t.Logf("FAILING Packet %d: len=%d bytes", i, len(pkt))
			t.Logf("  TOC: config=%d, stereo=%v, frameCode=%d", config, stereo, frameCode)
			t.Logf("  Parsed: mode=%d, bandwidth=%d, frameSize=%d", pktTOC.Mode, pktTOC.Bandwidth, pktTOC.FrameSize)

			// Frame code interpretation
			switch frameCode {
			case 0:
				t.Log("  Frame code 0: 1 frame")
			case 1:
				t.Log("  Frame code 1: 2 frames, equal size")
			case 2:
				t.Log("  Frame code 2: 2 frames, different size")
			case 3:
				if len(pkt) > 1 {
					frameByte := pkt[1]
					VBR := (frameByte & 0x80) != 0
					padding := (frameByte & 0x40) != 0
					count := int(frameByte & 0x3F)
					t.Logf("  Frame code 3: VBR=%v, padding=%v, count=%d", VBR, padding, count)
				}
			}

			t.Logf("  First 10 bytes: %X", pkt[:min(10, len(pkt))])
		}
	}
}

// TestMultiFramePackets tests specific multi-frame packets.
func TestMultiFramePackets(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector01.bit"

	packets, err := loadPacketsSimple(bitFile, 25)
	if err != nil {
		t.Skip("Could not load packets:", err)
	}

	// Focus on packets 1, 3 which are failing
	failingIndices := []int{1, 3, 8, 14, 16, 18}

	toc := gopus.ParseTOC(packets[0][0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}

	goDec, _ := gopus.NewDecoderDefault(48000, channels)

	for _, idx := range failingIndices {
		if idx >= len(packets) {
			continue
		}
		pkt := packets[idx]
		pktTOC := gopus.ParseTOC(pkt[0])

		t.Logf("\n=== Packet %d (len=%d) ===", idx, len(pkt))

		// Decode with Go
		goOut, err := goDec.DecodeFloat32(pkt)
		if err != nil {
			t.Logf("Go decode error: %v", err)
		} else {
			t.Logf("Go output: %d samples", len(goOut))
			t.Logf("  Expected per-frame: %d * channels=%d = %d", pktTOC.FrameSize, channels, pktTOC.FrameSize*channels)
			t.Logf("  Implied frames: %d", len(goOut)/(pktTOC.FrameSize*channels))
		}
	}
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
