// Package cgo provides detailed investigation of problematic hybrid packets
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestInvestigateHybridPackets looks at the specific packets causing testvector06 to fail
func TestInvestigateHybridPackets(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector06.bit"
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
		offset += 4 // skip encFinal
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	channels := 2

	// Create decoders
	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Process all packets, tracking worst ones
	type packetStats struct {
		idx     int
		snr     float64
		maxDiff float64
		toc     byte
		pktLen  int
	}

	var worst []packetStats

	// Decode in order to maintain state
	for i, pkt := range packets {
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := decodeFloat32(goDec, pkt)

		if libSamples <= 0 || len(goPcm) == 0 {
			continue
		}

		var sigPow, noisePow float64
		var maxDiff float64
		for j := 0; j < minInt(len(goPcm), libSamples*channels); j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			if math.Abs(noise) > maxDiff {
				maxDiff = math.Abs(noise)
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		if snr < 30 { // Capture packets with lower SNR
			worst = append(worst, packetStats{
				idx:     i,
				snr:     snr,
				maxDiff: maxDiff,
				toc:     pkt[0],
				pktLen:  len(pkt),
			})
		}
	}

	t.Logf("Found %d packets with SNR < 30 dB out of %d total", len(worst), len(packets))
	for _, w := range worst {
		toc := gopus.ParseTOC(w.toc)
		t.Logf("Packet %d: len=%d SNR=%.1f dB maxDiff=%.4f TOC=0x%02X mode=%v stereo=%v fs=%d",
			w.idx, w.pktLen, w.snr, w.maxDiff, w.toc, toc.Mode, toc.Stereo, toc.FrameSize)
	}

	// Now focus on the worst packet - decode with fresh state to see if it's a state issue
	if len(worst) > 0 {
		// Find worst packet
		worstIdx := 0
		worstSNR := float64(1000)
		for i, w := range worst {
			if w.snr < worstSNR {
				worstSNR = w.snr
				worstIdx = i
			}
		}
		w := worst[worstIdx]

		t.Logf("\nDetailed analysis of worst packet %d:", w.idx)

		// Decode with fresh decoder to see if state matters
		goDec2, _ := gopus.NewDecoderDefault(48000, channels)
		libDec2, _ := NewLibopusDecoder(48000, channels)
		defer libDec2.Destroy()

		// Warm up decoders
		for i := 0; i < w.idx; i++ {
			decodeFloat32(goDec2, packets[i])
			libDec2.DecodeFloat(packets[i], 5760)
		}

		// Decode the problematic packet
		goPcm, _ := decodeFloat32(goDec2, packets[w.idx])
		libPcm, libSamples := libDec2.DecodeFloat(packets[w.idx], 5760)

		t.Logf("Fresh decode - go samples: %d, lib samples: %d", len(goPcm)/channels, libSamples)

		// Show sample-by-sample comparison
		t.Logf("\nFirst 30 samples:")
		for j := 0; j < minInt(30, libSamples*channels); j++ {
			ch := "L"
			if j%2 == 1 {
				ch = "R"
			}
			diff := float64(goPcm[j]) - float64(libPcm[j])
			t.Logf("  [%3d] %s gopus=%10.6f libopus=%10.6f diff=%10.6f",
				j/2, ch, goPcm[j], libPcm[j], diff)
		}

		// Also show previous packet to check for state issues
		if w.idx > 0 {
			t.Logf("\nPrevious packet %d analysis:", w.idx-1)
			prevTOC := gopus.ParseTOC(packets[w.idx-1][0])
			t.Logf("  TOC: 0x%02X mode=%v stereo=%v fs=%d len=%d",
				packets[w.idx-1][0], prevTOC.Mode, prevTOC.Stereo, prevTOC.FrameSize, len(packets[w.idx-1]))
		}
	}
}

// TestTrackStateAcrossHybridPackets tracks de-emphasis state divergence
func TestTrackStateAcrossHybridPackets(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector06.bit"
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
	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Track how SNR evolves over time
	t.Logf("Tracking SNR evolution:")
	prevSNR := float64(100)

	for i, pkt := range packets {
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := decodeFloat32(goDec, pkt)

		if libSamples <= 0 || len(goPcm) == 0 {
			continue
		}

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

		// Log when SNR drops significantly
		if snr < prevSNR-10 || snr < 25 {
			toc := gopus.ParseTOC(pkt[0])
			t.Logf("Packet %d: SNR dropped to %.1f dB (was %.1f) - mode=%v fs=%d stereo=%v len=%d",
				i, snr, prevSNR, toc.Mode, toc.FrameSize, toc.Stereo, len(pkt))
		}

		prevSNR = snr

		// Stop after finding problematic region
		if i > 1550 {
			break
		}
	}
}
