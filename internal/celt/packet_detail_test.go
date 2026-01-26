package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestPacketDetails(t *testing.T) {
	testDir := filepath.Join("..", "testvectors", "testdata", "opus_testvectors")
	bitFile := filepath.Join(testDir, "testvector07.bit")

	bitData, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Test data not available: %v", err)
	}

	// Parse packets
	var packets [][]byte
	offset := 0
	for offset+8 <= len(bitData) {
		packetLen := binary.BigEndian.Uint32(bitData[offset:])
		offset += 8
		if offset+int(packetLen) > len(bitData) {
			break
		}
		packets = append(packets, bitData[offset:offset+int(packetLen)])
		offset += int(packetLen)
	}

	// Analyze packets around 18 and 31
	interestingPackets := []int{16, 17, 18, 19, 20, 29, 30, 31, 32, 33}

	for _, i := range interestingPackets {
		if i >= len(packets) {
			continue
		}
		pkt := packets[i]
		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		bw := getBandwidthType(cfg)

		// Parse packet header flags
		rd := &rangecoding.Decoder{}
		rd.Init(pkt[1:])

		totalBits := len(pkt[1:]) * 8
		tell := rd.Tell()
		silence := false
		if tell >= totalBits {
			silence = true
		} else if tell == 1 {
			silence = rd.DecodeBit(15) == 1
		}

		postfilter := false
		postfilterPeriod := 0
		if !silence && tell+16 <= totalBits {
			if rd.DecodeBit(1) == 1 {
				postfilter = true
				octave := int(rd.DecodeUniform(6))
				postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
				rd.DecodeRawBits(3) // gain
				if rd.Tell()+2 <= totalBits {
					rd.DecodeICDF([]uint8{2, 1, 0}, 2) // tapset
				}
			}
		}

		mode := GetModeConfig(frameSize)
		lm := mode.LM
		tell = rd.Tell()
		transient := false
		if lm > 0 && tell+3 <= totalBits {
			transient = rd.DecodeBit(3) == 1
		}
		tell = rd.Tell()
		intra := false
		if tell+3 <= totalBits {
			intra = rd.DecodeBit(3) == 1
		}

		fmt.Printf("Packet %2d: cfg=%2d, frameSize=%d, bw=%d, silence=%v, postfilter=%v (period=%d), transient=%v, intra=%v\n",
			i, cfg, frameSize, bw, silence, postfilter, postfilterPeriod, transient, intra)
	}
}
