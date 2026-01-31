// Package cgo investigates 2.5ms CELT frame handling issues
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestShortFrameHandling looks at how 2.5ms (120 sample) frames are decoded
func TestShortFrameHandling(t *testing.T) {
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

	// Find the first 2.5ms frame
	var firstShortIdx int
	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])
		if toc.FrameSize == 120 {
			firstShortIdx = i
			break
		}
	}

	t.Logf("First 2.5ms frame at packet %d", firstShortIdx)

	// Decode with fresh decoders starting a few frames before the short frame
	startIdx := firstShortIdx - 5
	if startIdx < 0 {
		startIdx = 0
	}

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Decode packets leading up to and including the first short frame
	t.Logf("\nAnalyzing packets %d to %d:", startIdx, firstShortIdx+3)

	for i := startIdx; i <= firstShortIdx+3 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := goDec.DecodeFloat32(pkt)

		if libSamples <= 0 || len(goPcm) == 0 {
			t.Logf("Packet %d: failed to decode", i)
			continue
		}

		var sigPow, noisePow float64
		var maxDiff float64
		var maxDiffIdx int
		for j := 0; j < minInt(len(goPcm), libSamples*channels); j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			if math.Abs(noise) > maxDiff {
				maxDiff = math.Abs(noise)
				maxDiffIdx = j
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		t.Logf("Packet %d: fs=%d samples=%d SNR=%.1f dB maxDiff=%.2e@%d TOC=0x%02X",
			i, toc.FrameSize, libSamples, snr, maxDiff, maxDiffIdx, pkt[0])

		// For the short frame, show sample details
		if toc.FrameSize == 120 && i <= firstShortIdx+2 {
			t.Logf("  First 20 samples:")
			for j := 0; j < 20 && j < libSamples; j++ {
				t.Logf("    [%3d] lib=%.8f go=%.8f diff=%.2e",
					j, libPcm[j], goPcm[j], float64(goPcm[j])-float64(libPcm[j]))
			}
			t.Logf("  Last 20 samples:")
			for j := libSamples - 20; j < libSamples && j >= 0; j++ {
				t.Logf("    [%3d] lib=%.8f go=%.8f diff=%.2e",
					j, libPcm[j], goPcm[j], float64(goPcm[j])-float64(libPcm[j]))
			}
		}
	}
}

// TestCompareShortFrameOverlap compares overlap handling for short frames
func TestCompareShortFrameOverlap(t *testing.T) {
	// The key issue is: for 2.5ms frames, frameSize = 120 = overlap
	// This means the IMDCT output and overlap region are the same size
	// The IMDCT produces 240 samples for 120-coeff input
	// Normal overlap-add: output = prev_overlap + IMDCT[0:overlap]
	//                     new_overlap = IMDCT[overlap:2*overlap]
	//
	// But for short frames: frameSize = overlap = 120
	// So output[0:120] = prev_overlap[0:120] + IMDCT[0:120]
	// And new_overlap = IMDCT[120:240]
	//
	// The question is: are we handling this correctly?

	t.Log("Analyzing short frame overlap handling:")
	t.Log("  - frameSize = 120 samples")
	t.Log("  - IMDCT input = 120 coefficients")
	t.Log("  - IMDCT output = 240 samples (2*input)")
	t.Log("  - overlap = 120 samples")
	t.Log("")
	t.Log("Expected overlap-add:")
	t.Log("  - output[0:120] = prev_overlap[0:120] + windowed(IMDCT[0:120])")
	t.Log("  - new_overlap = windowed(IMDCT[120:240])")

	// Check our synthesis code handles this case
}

// TestIsolateWorstPacket isolates the worst performing packet
func TestIsolateWorstPacket(t *testing.T) {
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

	// Packet 3307 was identified as worst (SNR=0.4 dB)
	// Let's decode up to that point
	targetIdx := 3307

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Decode all packets up to and including target
	for i := 0; i <= targetIdx && i < len(packets); i++ {
		libDec.DecodeFloat(packets[i], 5760)
		goDec.DecodeFloat32(packets[i])
	}

	// Now analyze packets around the worst one
	t.Logf("\nAnalyzing packets around worst packet %d:", targetIdx)

	// Create fresh decoders and decode up to targetIdx-5
	goDec2, _ := gopus.NewDecoder(48000, channels)
	libDec2, _ := NewLibopusDecoder(48000, channels)
	defer libDec2.Destroy()

	for i := 0; i < targetIdx-5; i++ {
		libDec2.DecodeFloat(packets[i], 5760)
		goDec2.DecodeFloat32(packets[i])
	}

	// Now decode the last 10 packets with detailed logging
	for i := targetIdx - 5; i <= targetIdx+3 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		libPcm, libSamples := libDec2.DecodeFloat(pkt, 5760)
		goPcm, _ := goDec2.DecodeFloat32(pkt)

		if libSamples <= 0 || len(goPcm) == 0 {
			continue
		}

		var sigPow, noisePow float64
		var maxDiff float64
		var maxDiffIdx int
		for j := 0; j < minInt(len(goPcm), libSamples*channels); j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			if math.Abs(noise) > maxDiff {
				maxDiff = math.Abs(noise)
				maxDiffIdx = j
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		marker := ""
		if i == targetIdx {
			marker = " <-- WORST"
		}

		t.Logf("Packet %d: fs=%d samples=%d SNR=%.1f dB maxDiff=%.2e@%d%s",
			i, toc.FrameSize, libSamples, snr, maxDiff, maxDiffIdx, marker)

		if i == targetIdx {
			// Show all samples
			t.Logf("  All %d samples of worst packet:", libSamples)
			for j := 0; j < libSamples; j++ {
				t.Logf("    [%3d] lib=%.8f go=%.8f diff=%.2e",
					j, libPcm[j], goPcm[j], float64(goPcm[j])-float64(libPcm[j]))
			}
		}
	}
}
