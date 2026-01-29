// Package cgo compares decoding parameters between gopus and libopus
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestComparePacket13vs14Params compares decoding between packets 13 and 14
func TestComparePacket13vs14Params(t *testing.T) {
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

	for _, pktIdx := range []int{13, 14} {
		pkt := packets[pktIdx]
		t.Logf("\n=== Packet %d ===", pktIdx)
		t.Logf("Length: %d, TOC: 0x%02X", len(pkt), pkt[0])

		dec := celt.NewDecoder(2)
		rd := &rangecoding.Decoder{}
		rd.Init(pkt[1:])

		silence := rd.DecodeBit(15)
		t.Logf("Silence: %d", silence)

		if silence == 0 {
			postfilter := rd.DecodeBit(1)
			t.Logf("Postfilter: %d", postfilter)
		}

		dec.SetRangeDecoder(rd)

		intensity, dualStereo := dec.DecodeStereoParams(21)
		t.Logf("Intensity: %d, DualStereo: %d", intensity, dualStereo)
		t.Logf("Tell after stereo: %d", rd.TellFrac())
	}
}
