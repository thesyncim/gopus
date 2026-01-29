// Package cgo investigates what happens around packet 1000 in testvector07
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestPacket1000Investigation investigates the state transition around packet 1000
func TestPacket1000Investigation(t *testing.T) {
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

	channels := 1

	// Look at packet headers around the transition
	t.Logf("Packet analysis around transition (990-1010):")
	for i := 990; i < 1010 && i < len(packets); i++ {
		pkt := packets[i]
		if len(pkt) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])
		t.Logf("  Packet %d: TOC=0x%02X mode=%d fs=%d len=%d",
			i, pkt[0], toc.Mode, toc.FrameSize, len(pkt))
	}

	// Create decoders and sync up to packet 995
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	t.Logf("\nDecoding packets 0-994 to sync state...")
	for i := 0; i < 995; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode 995-1005 with detailed analysis
	t.Logf("\nDetailed analysis of packets 995-1005:")
	for i := 995; i < 1006 && i < len(packets); i++ {
		pkt := packets[i]
		if len(pkt) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := goDec.DecodeFloat32(pkt)

		if libSamples <= 0 || len(goPcm) == 0 {
			t.Logf("Packet %d: decode failed", i)
			continue
		}

		// Calculate SNR
		var sigPow, noisePow float64
		for j := 0; j < minInt(len(goPcm), libSamples*channels); j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		t.Logf("Packet %d: TOC=0x%02X mode=%d fs=%d len=%d SNR=%.1f dB",
			i, pkt[0], toc.Mode, toc.FrameSize, len(pkt), snr)

		// For the transition packets, show sample details
		if i >= 999 && i <= 1001 {
			t.Logf("  First 5 samples:")
			for j := 0; j < 5 && j < libSamples; j++ {
				t.Logf("    [%d] lib=%.8f go=%.8f diff=%.2e",
					j, libPcm[j], goPcm[j], float64(goPcm[j])-float64(libPcm[j]))
			}
			t.Logf("  Last 5 samples:")
			for j := libSamples - 5; j < libSamples && j >= 0; j++ {
				t.Logf("    [%d] lib=%.8f go=%.8f diff=%.2e",
					j, libPcm[j], goPcm[j], float64(goPcm[j])-float64(libPcm[j]))
			}
		}
	}

	// Check what's different about packet 999 vs 1000
	t.Logf("\n\nComparing packet 999 and 1000 raw bytes:")
	pkt999 := packets[999]
	pkt1000 := packets[1000]

	t.Logf("Packet 999 (len=%d): %02X %02X %02X %02X %02X...",
		len(pkt999), pkt999[0], pkt999[1], pkt999[2], pkt999[3], pkt999[4])
	t.Logf("Packet 1000 (len=%d): %02X %02X %02X %02X %02X...",
		len(pkt1000), pkt1000[0], pkt1000[1], pkt1000[2], pkt1000[3], pkt1000[4])

	toc999 := gopus.ParseTOC(pkt999[0])
	toc1000 := gopus.ParseTOC(pkt1000[0])

	t.Logf("\nTOC comparison:")
	t.Logf("  999: config=%d, mode=%d, stereo=%v, fs=%d",
		(pkt999[0]>>3)&0x1F, toc999.Mode, toc999.Stereo, toc999.FrameSize)
	t.Logf("  1000: config=%d, mode=%d, stereo=%v, fs=%d",
		(pkt1000[0]>>3)&0x1F, toc1000.Mode, toc1000.Stereo, toc1000.FrameSize)
}

// TestWithFreshDecodersAtPacket1000 tries decoding packet 1000 with fresh state
func TestWithFreshDecodersAtPacket1000(t *testing.T) {
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

	channels := 1

	// Test 1: Decode packets 1000-1003 with fresh decoders
	t.Logf("Test 1: Fresh decoders starting at packet 1000")
	goDec1, _ := gopus.NewDecoder(48000, channels)
	libDec1, _ := NewLibopusDecoder(48000, channels)
	defer libDec1.Destroy()

	for i := 1000; i < 1004; i++ {
		libPcm, libSamples := libDec1.DecodeFloat(packets[i], 5760)
		goPcm, _ := goDec1.DecodeFloat32(packets[i])

		var sigPow, noisePow float64
		for j := 0; j < minInt(len(goPcm), libSamples*channels); j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}
		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}
		t.Logf("  Packet %d: SNR=%.1f dB", i, snr)
	}

	// Test 2: Sync decoders up to 999, then decode 1000+
	t.Logf("\nTest 2: Synced decoders (0-999), then decode 1000+")
	goDec2, _ := gopus.NewDecoder(48000, channels)
	libDec2, _ := NewLibopusDecoder(48000, channels)
	defer libDec2.Destroy()

	for i := 0; i < 1000; i++ {
		goDec2.DecodeFloat32(packets[i])
		libDec2.DecodeFloat(packets[i], 5760)
	}

	for i := 1000; i < 1004; i++ {
		libPcm, libSamples := libDec2.DecodeFloat(packets[i], 5760)
		goPcm, _ := goDec2.DecodeFloat32(packets[i])

		var sigPow, noisePow float64
		for j := 0; j < minInt(len(goPcm), libSamples*channels); j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}
		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}
		t.Logf("  Packet %d: SNR=%.1f dB", i, snr)
	}
}
