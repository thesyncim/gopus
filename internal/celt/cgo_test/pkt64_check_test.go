// pkt64_check_test.go - Check transient flag for packets 61-65
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestPacket61_65TransientFlags(t *testing.T) {
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

	for i := 61; i <= 65 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		t.Logf("Packet %d (len=%d): mode=%v frame=%d stereo=%v", i, len(pkt), toc.Mode, toc.FrameSize, toc.Stereo)

		if toc.Mode != gopus.ModeCELT {
			t.Log("  Not CELT mode")
			continue
		}

		celtData := pkt[1:]
		rd := &rangecoding.Decoder{}
		rd.Init(celtData)

		mode := celt.GetModeConfig(toc.FrameSize)
		lm := mode.LM
		totalBits := len(celtData) * 8

		tell := rd.Tell()

		// Check silence flag
		silence := false
		if tell >= totalBits {
			silence = true
		} else if tell == 1 {
			silence = rd.DecodeBit(15) == 1
		}
		t.Logf("  silence=%v", silence)

		if silence {
			continue
		}

		// Check postfilter
		if tell+16 <= totalBits {
			if rd.DecodeBit(1) == 1 {
				// Skip postfilter params
				octave := int(rd.DecodeUniform(6))
				_ = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
				_ = rd.DecodeRawBits(3)
				if rd.Tell()+2 <= totalBits {
					_ = rd.DecodeRawBits(2)
				}
			}
			tell = rd.Tell()
		}

		// Check transient flag
		transient := false
		if lm > 0 && tell+3 <= totalBits {
			transient = rd.DecodeBit(3) == 1
		}
		t.Logf("  transient=%v (lm=%d)", transient, lm)

		shortBlocks := 1
		if transient {
			shortBlocks = mode.ShortBlocks
		}
		t.Logf("  shortBlocks=%d", shortBlocks)
	}
}
