//go:build cgo_libopus
// +build cgo_libopus

// Package cgo analyzes packet flags for 996-1005
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestPacketFlagsAnalysis analyzes the flags in packets 996-1005
func TestPacketFlagsAnalysis(t *testing.T) {
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

	t.Log("Packet flags analysis for 996-1005:")
	t.Log("Pkt | FS  | Len | Silence | Postfilter | Period | Gain  | Intra")
	t.Log("----|-----|-----|---------|------------|--------|-------|------")

	for i := 996; i < 1010 && i < len(packets); i++ {
		pkt := packets[i]
		if len(pkt) <= 1 {
			continue
		}

		toc := gopus.ParseTOC(pkt[0])
		celtData := pkt[1:]

		rd := &rangecoding.Decoder{}
		rd.Init(celtData)

		totalBits := len(celtData) * 8
		tell := rd.Tell()

		// Decode silence
		silence := false
		if tell >= totalBits {
			silence = true
		} else if tell == 1 {
			silence = rd.DecodeBit(15) == 1
		}

		// Decode postfilter
		pfEnabled := false
		pfPeriod := 0
		pfGain := 0.0
		if !silence && rd.Tell()+16 <= totalBits {
			if rd.DecodeBit(1) == 1 {
				pfEnabled = true
				octave := int(rd.DecodeUniform(6))
				pfPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
				qg := int(rd.DecodeRawBits(3))
				if rd.Tell()+2 <= totalBits {
					_ = rd.DecodeICDF([]uint8{2, 1, 0}, 2) // tapset
				}
				pfGain = 0.09375 * float64(qg+1)
			}
		}

		// For LM=0, no transient flag
		// Decode intra
		intra := false
		if rd.Tell()+3 <= totalBits {
			intra = rd.DecodeBit(3) == 1
		}

		t.Logf(" %4d | %3d |  %2d |    %v   |      %v     |   %3d  | %.4f |  %v",
			i, toc.FrameSize, len(pkt), silence, pfEnabled, pfPeriod, pfGain, intra)
	}
}
