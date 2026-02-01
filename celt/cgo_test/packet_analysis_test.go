//go:build cgo_libopus
// +build cgo_libopus

// Package cgo analyzes packet structure differences
package cgo

import (
	"encoding/binary"
	"os"
	"testing"
)

// TestAnalyzePacketDifferences compares packet structure between working and failing packets
func TestAnalyzePacketDifferences(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector08.bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	// Compare packets 13 (last working CELT) and 14 (first failing CELT)
	for _, pktIdx := range []int{5, 13, 14, 15} {
		if pktIdx >= len(packets) {
			continue
		}
		pkt := packets[pktIdx]
		toc := pkt[0]
		config := toc >> 3
		stereo := (toc >> 2) & 1
		frameCode := toc & 3

		// Frame duration mapping for config 16-31 (CELT-only)
		frameDuration := []string{"2.5ms", "5ms", "10ms", "20ms"}[config%4]

		t.Logf("Packet %d:", pktIdx)
		t.Logf("  Length: %d bytes", len(pkt))
		t.Logf("  TOC: 0x%02X (config=%d, stereo=%d, frames=%d)", toc, config, stereo, frameCode)
		t.Logf("  Mode: CELT %s", frameDuration)
		t.Logf("  First 10 bytes: %02X", pkt[:min(10, len(pkt))])

		// Analyze byte differences with previous packet if available
		if pktIdx > 0 && pktIdx-1 < len(packets) {
			prevPkt := packets[pktIdx-1]
			if len(pkt) > 1 && len(prevPkt) > 1 {
				// Skip TOC, compare payload
				pktPayload := pkt[1:]
				prevPayload := prevPkt[1:]

				matches := 0
				minLen := min(len(pktPayload), len(prevPayload))
				for i := 0; i < minLen; i++ {
					if pktPayload[i] == prevPayload[i] {
						matches++
					}
				}
				t.Logf("  Payload similarity to prev: %d/%d bytes match", matches, minLen)
			}
		}
		t.Log("")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
