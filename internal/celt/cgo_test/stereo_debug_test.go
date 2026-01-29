// Package cgo tests stereo merge debug output
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// TestStereoMergeDebug enables debug tracing and decodes packet 14
func TestStereoMergeDebug(t *testing.T) {
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

	// Enable debug tracing
	celt.DebugStereoMerge = true
	defer func() { celt.DebugStereoMerge = false }()

	// Decode packet 14 with fresh decoder
	pkt := packets[14]
	t.Logf("Decoding packet 14: len=%d, TOC=0x%02X", len(pkt), pkt[0])

	goDec, _ := gopus.NewDecoder(48000, 2)
	goSamples, _ := goDec.DecodeFloat32(pkt)

	t.Logf("Got %d samples", len(goSamples)/2)
}
