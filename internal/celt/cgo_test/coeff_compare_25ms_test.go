// Package cgo compares coefficient decoding for 2.5ms frames
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// TestCompareCoefficients25ms compares decoded CELT coefficients for 2.5ms frames
func TestCompareCoefficients25ms(t *testing.T) {
	// Read testvector07 which has 2.5ms frames
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

	// Find the first 2.5ms CELT packet after syncing decoders
	channels := 1
	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Decode some frames to sync state, then analyze a 2.5ms frame
	var target2_5msIdx int
	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])
		if toc.FrameSize == 120 { // 2.5ms
			target2_5msIdx = i
			break
		}
		// Decode to sync state
		decodeFloat32(goDec, pkt)
		libDec.DecodeFloat(pkt, 5760)
	}

	if target2_5msIdx == 0 {
		t.Skip("No 2.5ms frame found")
		return
	}

	t.Logf("Found first 2.5ms frame at packet %d", target2_5msIdx)

	// Now decode the 2.5ms frame and compare outputs
	pkt := packets[target2_5msIdx]
	toc := gopus.ParseTOC(pkt[0])

	t.Logf("Packet details: TOC=0x%02X, mode=%v, fs=%d, len=%d",
		pkt[0], toc.Mode, toc.FrameSize, len(pkt))

	// Decode with both
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	goPcm, _ := decodeFloat32(goDec, pkt)

	if libSamples <= 0 || len(goPcm) == 0 {
		t.Fatal("Failed to decode")
	}

	// Compare all samples
	t.Logf("\nAll %d samples comparison:", libSamples)
	var sigPow, noisePow float64
	var maxDiff float64
	var maxDiffIdx int

	for i := 0; i < libSamples; i++ {
		lib := float64(libPcm[i])
		go_ := float64(goPcm[i])
		diff := go_ - lib

		sigPow += lib * lib
		noisePow += diff * diff

		if math.Abs(diff) > maxDiff {
			maxDiff = math.Abs(diff)
			maxDiffIdx = i
		}
	}

	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("SNR: %.1f dB, maxDiff: %.4f at sample %d", snr, maxDiff, maxDiffIdx)

	// Show samples around max diff
	start := maxDiffIdx - 10
	if start < 0 {
		start = 0
	}
	end := maxDiffIdx + 10
	if end > libSamples {
		end = libSamples
	}
	t.Logf("\nSamples around max diff [%d:%d]:", start, end)
	for i := start; i < end; i++ {
		t.Logf("  [%3d] lib=%.8f go=%.8f diff=%.6f",
			i, libPcm[i], goPcm[i], float64(goPcm[i])-float64(libPcm[i]))
	}

	// Now let's look at consecutive 2.5ms frames to see error growth
	t.Logf("\n\nAnalyzing consecutive 2.5ms frames starting at packet %d:", target2_5msIdx)

	for i := target2_5msIdx; i < target2_5msIdx+10 && i < len(packets); i++ {
		pkt := packets[i]
		if len(pkt) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := decodeFloat32(goDec, pkt)

		if libSamples <= 0 || len(goPcm) == 0 {
			continue
		}

		var sigPow, noisePow float64
		var maxDiff float64
		for j := 0; j < libSamples; j++ {
			lib := float64(libPcm[j])
			go_ := float64(goPcm[j])
			sigPow += lib * lib
			noisePow += (go_ - lib) * (go_ - lib)
			if d := math.Abs(go_ - lib); d > maxDiff {
				maxDiff = d
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		t.Logf("Packet %d: fs=%d SNR=%.1f dB maxDiff=%.4f",
			i, toc.FrameSize, snr, maxDiff)
	}
}

// TestExtractCELTCoeffs tries to extract and compare CELT coefficients directly
func TestExtractCELTCoeffs(t *testing.T) {
	// This test tries to isolate where divergence occurs in CELT decoding
	// We'll decode a 2.5ms frame step by step

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

	// Find a 2.5ms frame
	var pkt25ms []byte
	var pkt25msIdx int
	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])
		if toc.FrameSize == 120 {
			pkt25ms = pkt
			pkt25msIdx = i
			break
		}
	}

	if pkt25ms == nil {
		t.Skip("No 2.5ms frame found")
		return
	}

	t.Logf("Analyzing 2.5ms packet %d (len=%d, TOC=0x%02X)", pkt25msIdx, len(pkt25ms), pkt25ms[0])

	// Parse the packet structure
	// For CELT frames, the data after TOC byte contains:
	// 1. Optional silence flag (1 bit for stereo, 0 for mono)
	// 2. Post-filter params if postfilter enabled
	// 3. Transient flag (if 20ms frame)
	// 4. Coarse energy
	// 5. Fine energy
	// 6. Band coefficients

	// Create a fresh CELT decoder to examine internal state
	celtDec := celt.NewDecoder(1)
	celtDec.Reset()

	// Strip TOC byte for CELT decoding
	celtData := pkt25ms[1:]

	// Decode the frame
	samples, err := celtDec.DecodeFrame(celtData, 120)
	if err != nil {
		t.Fatalf("Failed to decode CELT frame: %v", err)
	}

	t.Logf("Decoded %d samples", len(samples))

	// Show first and last few samples
	t.Logf("\nFirst 10 samples from CELT decoder:")
	for i := 0; i < 10 && i < len(samples); i++ {
		t.Logf("  [%d] = %.8f", i, samples[i])
	}
}
