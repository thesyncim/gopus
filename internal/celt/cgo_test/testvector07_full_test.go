// Package cgo analyzes testvector07 error progression fully
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTestvector07FullAnalysis analyzes ALL packets in testvector07
func TestTestvector07FullAnalysis(t *testing.T) {
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
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	t.Logf("Total packets: %d", len(packets))

	// Track error accumulation
	var worstSNR float64 = 999
	var worstIdx int
	var sumSigPow, sumNoisePow float64

	// Count different frame sizes
	frameSizes := make(map[int]int)

	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}

		toc := gopus.ParseTOC(pkt[0])
		frameSizes[toc.FrameSize]++

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

		sumSigPow += sigPow
		sumNoisePow += noisePow

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		// Log when SNR is particularly bad
		if snr < worstSNR {
			worstSNR = snr
			worstIdx = i
		}

		// Log every packet with SNR < 50 dB
		if snr < 50 {
			t.Logf("Packet %d: SNR=%.1f dB TOC=0x%02X mode=%v fs=%d len=%d",
				i, snr, pkt[0], toc.Mode, toc.FrameSize, len(pkt))
		}

		// Log every 500th packet to show progression
		if i%500 == 0 {
			cumulativeSNR := 10 * math.Log10(sumSigPow/sumNoisePow)
			t.Logf("Packet %d: cumulative SNR=%.1f dB, this packet SNR=%.1f dB",
				i, cumulativeSNR, snr)
		}
	}

	overallSNR := 10 * math.Log10(sumSigPow/sumNoisePow)
	t.Logf("\nOverall SNR: %.1f dB", overallSNR)
	t.Logf("Worst packet: %d with SNR=%.1f dB", worstIdx, worstSNR)
	t.Logf("Q metric: %.2f (need >= 0)", (overallSNR-48)*100/48)

	t.Logf("\nFrame size distribution:")
	for fs, count := range frameSizes {
		t.Logf("  %d samples: %d packets", fs, count)
	}

	if overallSNR < 48 {
		t.Errorf("Overall SNR %.1f dB is below threshold (48 dB)", overallSNR)
	}
}

// TestTestvector07FindTransients specifically looks for transient frames
func TestTestvector07FindTransients(t *testing.T) {
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

	t.Logf("Total packets: %d", len(packets))

	// Decode with libopus and check for frames with different block structure
	// The transient flag is inside the CELT bitstream, not visible from TOC
	// But 20ms CELT frames (fs=960) can have transients

	t.Logf("\nLooking for 20ms CELT frames (potential transients):")
	count := 0
	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])
		// Mode 2 = CELT, and various frame sizes
		if toc.Mode == 2 && toc.FrameSize == 960 {
			if count < 20 {
				t.Logf("Packet %d: TOC=0x%02X fs=%d len=%d", i, pkt[0], toc.FrameSize, len(pkt))
			}
			count++
		}
	}
	t.Logf("Found %d 20ms CELT frames total", count)
}
