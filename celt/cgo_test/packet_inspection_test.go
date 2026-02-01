//go:build cgo_libopus
// +build cgo_libopus

// packet_inspection_test.go - Inspect packets around the transition point
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestInspectPackets59To64 checks the flags of packets 59-64
func TestInspectPackets59To64(t *testing.T) {
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

	t.Log("Inspecting packets 55-65:")
	t.Log("Pkt | Len | Frame | Stereo | Transient | PostFilter | Intra")
	t.Log("----+-----+-------+--------+-----------+------------+------")

	for i := 55; i <= 65 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeCELT {
			t.Logf(" %2d | %3d | %5d |   %v   |    N/A    |     N/A    |  N/A",
				i, len(pkt), toc.FrameSize, toc.Stereo)
			continue
		}

		celtData := pkt[1:]
		rd := &rangecoding.Decoder{}
		rd.Init(celtData)

		mode := celt.GetModeConfig(toc.FrameSize)
		lm := mode.LM
		totalBits := len(celtData) * 8
		tell := rd.Tell()

		// Silence
		silence := tell >= totalBits
		if !silence && tell == 1 {
			silence = rd.DecodeBit(15) == 1
		}

		if silence {
			t.Logf(" %2d | %3d | %5d |   %v   |    silent", i, len(pkt), toc.FrameSize, toc.Stereo)
			continue
		}

		// Postfilter
		postfilter := false
		if tell+16 <= totalBits {
			postfilter = rd.DecodeBit(1) == 1
			if postfilter {
				octave := int(rd.DecodeUniform(6))
				_ = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
				_ = rd.DecodeRawBits(3)
				if rd.Tell()+2 <= totalBits {
					_ = rd.DecodeRawBits(2)
				}
			}
			tell = rd.Tell()
		}

		// Transient
		transient := false
		if lm > 0 && tell+3 <= totalBits {
			transient = rd.DecodeBit(3) == 1
			tell = rd.Tell()
		}

		// Intra
		intra := false
		if tell+3 <= totalBits {
			intra = rd.DecodeBit(3) == 1
		}

		transientStr := " "
		if transient {
			transientStr = "T"
		}

		postfilterStr := " "
		if postfilter {
			postfilterStr = "P"
		}

		intraStr := " "
		if intra {
			intraStr = "I"
		}

		t.Logf(" %2d | %3d | %5d |   %v   |     %s     |      %s     |   %s",
			i, len(pkt), toc.FrameSize, toc.Stereo, transientStr, postfilterStr, intraStr)
	}
}

// TestComparePacket59And60 compares decoding with/without packet 59 and 60
func TestComparePacket59And60(t *testing.T) {
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

	// Test A: Skip packet 59, decode 60, then 61
	t.Log("Testing effect of skipping packet 59:")
	goDecA, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	libDecA, _ := NewLibopusDecoder(48000, 2)
	defer libDecA.Destroy()

	for i := 0; i < 59; i++ {
		decodeFloat32(goDecA, packets[i])
		libDecA.DecodeFloat(packets[i], 5760)
	}
	// Skip 59, decode 60
	decodeFloat32(goDecA, packets[60])
	libDecA.DecodeFloat(packets[60], 5760)
	// Decode 61
	goPcmA, _ := decodeFloat32(goDecA, packets[61])
	libPcmA, libNA := libDecA.DecodeFloat(packets[61], 5760)

	nA := minInt(len(goPcmA), libNA*2)
	var sigA, noiseA float64
	for j := 0; j < nA; j++ {
		s := float64(libPcmA[j])
		d := float64(goPcmA[j]) - s
		sigA += s * s
		noiseA += d * d
	}
	snrA := 10 * math.Log10(sigA/noiseA)
	libMemA, _ := libDecA.GetPreemphState()
	goStateA := goDecA.GetCELTDecoder().PreemphState()
	t.Logf("  Skip 59: SNR=%.1f dB, state_err=%.6f", snrA, absf64(goStateA[0]-float64(libMemA)))

	// Test B: Decode 59, skip 60, decode 61
	t.Log("Testing effect of skipping packet 60:")
	goDecB, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	libDecB, _ := NewLibopusDecoder(48000, 2)
	defer libDecB.Destroy()

	for i := 0; i < 60; i++ {
		decodeFloat32(goDecB, packets[i])
		libDecB.DecodeFloat(packets[i], 5760)
	}
	// Skip 60, decode 61 directly
	goPcmB, _ := decodeFloat32(goDecB, packets[61])
	libPcmB, libNB := libDecB.DecodeFloat(packets[61], 5760)

	nB := minInt(len(goPcmB), libNB*2)
	var sigB, noiseB float64
	for j := 0; j < nB; j++ {
		s := float64(libPcmB[j])
		d := float64(goPcmB[j]) - s
		sigB += s * s
		noiseB += d * d
	}
	snrB := 10 * math.Log10(sigB/noiseB)
	libMemB, _ := libDecB.GetPreemphState()
	goStateB := goDecB.GetCELTDecoder().PreemphState()
	t.Logf("  Skip 60: SNR=%.1f dB, state_err=%.6f", snrB, absf64(goStateB[0]-float64(libMemB)))

	// Test C: Normal decode 59, 60, 61
	t.Log("Testing normal decode (59, 60, 61):")
	goDecC, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	libDecC, _ := NewLibopusDecoder(48000, 2)
	defer libDecC.Destroy()

	for i := 0; i <= 60; i++ {
		decodeFloat32(goDecC, packets[i])
		libDecC.DecodeFloat(packets[i], 5760)
	}
	goPcmC, _ := decodeFloat32(goDecC, packets[61])
	libPcmC, libNC := libDecC.DecodeFloat(packets[61], 5760)

	nC := minInt(len(goPcmC), libNC*2)
	var sigC, noiseC float64
	for j := 0; j < nC; j++ {
		s := float64(libPcmC[j])
		d := float64(goPcmC[j]) - s
		sigC += s * s
		noiseC += d * d
	}
	snrC := 10 * math.Log10(sigC/noiseC)
	libMemC, _ := libDecC.GetPreemphState()
	goStateC := goDecC.GetCELTDecoder().PreemphState()
	t.Logf("  Normal: SNR=%.1f dB, state_err=%.6f", snrC, absf64(goStateC[0]-float64(libMemC)))
}

func absf64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
