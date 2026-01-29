// Package cgo examines CELT packet content differences
package cgo

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestExaminePacketContents examines the internal structure of packets 999-1003
func TestExaminePacketContents(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
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

	// Analyze packets 999-1003
	for i := 999; i <= 1003 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		t.Logf("\n=== Packet %d ===", i)
		t.Logf("TOC: 0x%02X (mode=%d, fs=%d, stereo=%v)", pkt[0], toc.Mode, toc.FrameSize, toc.Stereo)
		t.Logf("Length: %d bytes", len(pkt))
		t.Logf("Raw bytes: %s", formatBytes(pkt))

		// Try to decode CELT header flags
		if len(pkt) > 1 {
			// CELT data starts after TOC
			celtData := pkt[1:]

			// For mono, first thing might be silence flag (1 bit)
			// Actually for CELT the structure is:
			// - Silence flag (mono: 0 bits, stereo: 1 bit)
			// - Post-filter params (optional)
			// - Transient flag (only for long frames)
			// - Intra flag
			// - Coarse energy

			// Read first few bits to check structure
			// The range coder starts with the raw bits
			t.Logf("First byte after TOC: 0x%02X (binary: %08b)", celtData[0], celtData[0])
			if len(celtData) > 1 {
				t.Logf("Second byte after TOC: 0x%02X (binary: %08b)", celtData[1], celtData[1])
			}

			// The structure of CELT frames includes:
			// - No silence flag (mono)
			// - Post-filter (if enabled)
			// - Intra frame (1 bit) - signals whether to use intra energy coding
			// - Coarse energy (variable)

			// The intra flag is important - it resets energy state
		}
	}
}

func formatBytes(data []byte) string {
	if len(data) > 20 {
		return fmt.Sprintf("%02X ... (%d more bytes)", data[:10], len(data)-10)
	}
	result := ""
	for i, b := range data {
		if i > 0 {
			result += " "
		}
		result += fmt.Sprintf("%02X", b)
	}
	return result
}

// TestCompareEnergyCoding compares energy decoding between packets
func TestCompareEnergyCoding(t *testing.T) {
	// This test would require access to CELT internal state
	// For now, just note that the issue may be in energy decoding
	t.Log("Energy decoding comparison would require CELT internal access")
	t.Log("Key hypothesis: packets 1000-1002 may use inter-frame energy prediction")
	t.Log("that accumulates state differently between gopus and libopus")
}
