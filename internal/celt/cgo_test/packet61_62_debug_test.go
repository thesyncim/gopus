// packet61_62_debug_test.go - Debug packets 61-62 which cause divergence
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestDecodePacket61_62Headers examines header flags of packets 61-62
func TestDecodePacket61_62Headers(t *testing.T) {
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

	// Decode header flags for packets 59-63
	for i := 59; i <= 63 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		t.Logf("\n=== Packet %d (len=%d) ===", i, len(pkt))
		t.Logf("TOC: mode=%v, frameSize=%d, stereo=%v", toc.Mode, toc.FrameSize, toc.Stereo)

		// Parse the CELT header
		if toc.Mode != gopus.ModeCELT {
			t.Logf("  Not CELT mode, skipping header parse")
			continue
		}

		// For CELT mode, the frame data follows the TOC
		celtData := pkt[1:]
		if len(celtData) == 0 {
			continue
		}

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
		hasPostfilter := false
		postfilterPeriod := 0
		postfilterGain := 0.0
		postfilterTapset := 0
		if tell+16 <= totalBits {
			if rd.DecodeBit(1) == 1 {
				hasPostfilter = true
				octave := int(rd.DecodeUniform(6))
				postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
				qg := int(rd.DecodeRawBits(3))
				if rd.Tell()+2 <= totalBits {
					postfilterTapset = int(rd.DecodeRawBits(2)) // Simplified
				}
				postfilterGain = 0.09375 * float64(qg+1)
			}
			tell = rd.Tell()
		}
		t.Logf("  postfilter=%v period=%d gain=%.4f tapset=%d", hasPostfilter, postfilterPeriod, postfilterGain, postfilterTapset)

		// Check transient flag
		transient := false
		if lm > 0 && tell+3 <= totalBits {
			transient = rd.DecodeBit(3) == 1
			tell = rd.Tell()
		}
		t.Logf("  transient=%v (lm=%d)", transient, lm)

		// Check intra flag
		intra := false
		if tell+3 <= totalBits {
			intra = rd.DecodeBit(3) == 1
		}
		t.Logf("  intra=%v", intra)

		// Log shortBlocks
		shortBlocks := 1
		if transient {
			shortBlocks = mode.ShortBlocks
		}
		t.Logf("  shortBlocks=%d", shortBlocks)
	}
}

// TestComparePacket61_62WithLibopus compares internal state at packets 61-62
func TestComparePacket61_62WithLibopus(t *testing.T) {
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

	channels := 2

	// Decode up to packet 60 with both decoders to get state aligned
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Decode packets 0-60
	for i := 0; i <= 60; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode packet 61 and compare in detail
	pkt61 := packets[61]
	t.Logf("\n=== Decoding packet 61 ===")
	t.Logf("Packet length: %d bytes", len(pkt61))
	t.Logf("TOC byte: 0x%02x", pkt61[0])

	goPcm61, _ := goDec.DecodeFloat32(pkt61)
	libPcm61, _ := libDec.DecodeFloat(pkt61, 5760)

	// Compare samples at different positions
	t.Log("Sample comparison at key positions:")
	positions := []int{0, 100, 200, 500, 700, 720, 730, 740, 800, 900}
	for _, pos := range positions {
		idx := pos * 2 // stereo interleaved
		if idx+1 < len(goPcm61) && idx+1 < len(libPcm61)*2 {
			goL := goPcm61[idx]
			goR := goPcm61[idx+1]
			libL := libPcm61[idx]
			libR := libPcm61[idx+1]
			diffL := goL - libL
			diffR := goR - libR
			t.Logf("  Sample %d: goL=%.8f libL=%.8f diffL=%.8f", pos, goL, libL, diffL)
			t.Logf("            goR=%.8f libR=%.8f diffR=%.8f", goR, libR, diffR)
		}
	}

	// Now decode packet 62
	pkt62 := packets[62]
	t.Logf("\n=== Decoding packet 62 ===")
	t.Logf("Packet length: %d bytes", len(pkt62))

	goPcm62, _ := goDec.DecodeFloat32(pkt62)
	libPcm62, _ := libDec.DecodeFloat(pkt62, 5760)

	t.Log("Sample comparison at key positions:")
	for _, pos := range positions {
		idx := pos * 2
		if idx+1 < len(goPcm62) && idx+1 < len(libPcm62)*2 {
			goL := goPcm62[idx]
			goR := goPcm62[idx+1]
			libL := libPcm62[idx]
			libR := libPcm62[idx+1]
			diffL := goL - libL
			diffR := goR - libR
			t.Logf("  Sample %d: goL=%.8f libL=%.8f diffL=%.8f", pos, goL, libL, diffL)
			t.Logf("            goR=%.8f libR=%.8f diffR=%.8f", goR, libR, diffR)
		}
	}
}
