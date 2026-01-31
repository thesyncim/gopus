// Package cgo compares short block IMDCT output between gopus and libopus
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// TestCompareWindowCoefficients compares window coefficients with libopus formula
func TestCompareWindowCoefficients(t *testing.T) {
	const overlap = 120

	// Get gopus window
	goWin := celt.GetWindowBuffer(overlap)

	// Compare with libopus computed window
	maxDiff, maxDiffIdx := CompareWindowCoefficients(overlap, goWin)

	t.Logf("Max window diff: %.2e at index %d", maxDiff, maxDiffIdx)

	// Show first 10 and last 10 window values
	libWin := ComputeLibopusWindow(overlap)

	t.Logf("\nFirst 10 window values:")
	for i := 0; i < 10; i++ {
		t.Logf("  [%3d] lib=%.10f go=%.10f diff=%.2e", i, libWin[i], goWin[i], goWin[i]-libWin[i])
	}

	t.Logf("\nMiddle window values (55-65):")
	for i := 55; i < 65; i++ {
		t.Logf("  [%3d] lib=%.10f go=%.10f diff=%.2e", i, libWin[i], goWin[i], goWin[i]-libWin[i])
	}

	if maxDiff > 1e-7 {
		t.Errorf("Window coefficient mismatch: max diff = %.2e at index %d", maxDiff, maxDiffIdx)
	}
}

// TestShortBlockSynthesisViaDecoder compares short block synthesis via full decoding
func TestShortBlockSynthesisViaDecoder(t *testing.T) {
	// Use testvector07 which has transient (short block) frames
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
		offset += 4 // skip encFinal
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	// testvector07 is mono
	channels := 1

	// Create decoders
	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Find transient frames by looking at TOC byte
	t.Logf("Analyzing transient frames in testvector07...")

	// Track error progression for transient vs non-transient frames
	transientCount := 0
	normalCount := 0

	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}

		toc := gopus.ParseTOC(pkt[0])

		// CELT mode with config 28-31 indicates short blocks possible
		// Actually, transient is signaled in the frame data, not TOC
		// Let's just compare all frames and note which have higher error

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := decodeFloat32(goDec, pkt)

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

		// Log packets with SNR below 80 dB
		if snr < 80 {
			t.Logf("Packet %d: SNR=%.1f dB maxDiff=%.4f@%d TOC=0x%02X mode=%v fs=%d len=%d",
				i, snr, maxDiff, maxDiffIdx, pkt[0], toc.Mode, toc.FrameSize, len(pkt))

			// For low SNR packets, show sample-level details at the boundary
			if snr < 50 && maxDiffIdx > 0 {
				start := maxDiffIdx - 5
				if start < 0 {
					start = 0
				}
				end := maxDiffIdx + 5
				if end > libSamples {
					end = libSamples
				}
				t.Logf("  Samples around max diff [%d-%d]:", start, end)
				for j := start; j < end; j++ {
					t.Logf("    [%4d] lib=%.6f go=%.6f diff=%.6f",
						j, libPcm[j], goPcm[j], float64(goPcm[j])-float64(libPcm[j]))
				}
			}

			transientCount++
		} else {
			normalCount++
		}

		// Stop after first 100 packets for initial analysis
		if i > 100 {
			break
		}
	}

	t.Logf("\nSummary: %d packets with SNR < 80 dB, %d with SNR >= 80 dB", transientCount, normalCount)
}

// TestIsolateTransientFrame looks at a single transient frame in detail
func TestIsolateTransientFrame(t *testing.T) {
	// Use testvector07 which has transient frames
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

	// Decode packets until we find a transient frame (low SNR)
	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Find first problematic frame
	var problemIdx int
	var prevSNR float64 = 999

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

		// Find first frame where SNR drops significantly
		if prevSNR > 100 && snr < 80 {
			problemIdx = i
			t.Logf("Found first problematic frame at packet %d (SNR dropped from %.1f to %.1f dB)",
				i, prevSNR, snr)
			break
		}
		prevSNR = snr
	}

	if problemIdx == 0 {
		t.Log("No problematic frame found in first pass")
		return
	}

	// Now create fresh decoders and decode up to and including the problem frame
	goDec2, _ := gopus.NewDecoderDefault(48000, channels)
	libDec2, _ := NewLibopusDecoder(48000, channels)
	defer libDec2.Destroy()

	// Decode frames before the problem to sync state
	for i := 0; i < problemIdx; i++ {
		decodeFloat32(goDec2, packets[i])
		libDec2.DecodeFloat(packets[i], 5760)
	}

	// Now decode the problematic frame with detailed analysis
	t.Logf("\nDetailed analysis of packet %d:", problemIdx)

	libPcm, libSamples := libDec2.DecodeFloat(packets[problemIdx], 5760)
	goPcm, _ := decodeFloat32(goDec2, packets[problemIdx])

	toc := gopus.ParseTOC(packets[problemIdx][0])
	t.Logf("TOC=0x%02X mode=%v fs=%d len=%d", packets[problemIdx][0], toc.Mode, toc.FrameSize, len(packets[problemIdx]))

	// Analyze error by 120-sample blocks (short block size)
	blockSize := 120
	numBlocks := libSamples / blockSize

	t.Logf("\nBlock-by-block analysis (%d blocks of %d samples):", numBlocks, blockSize)
	for b := 0; b < numBlocks; b++ {
		start := b * blockSize
		end := start + blockSize

		var sigPow, noisePow float64
		var maxDiff float64
		var maxDiffIdx int

		for j := start; j < end && j < len(goPcm); j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			if math.Abs(noise) > maxDiff {
				maxDiff = math.Abs(noise)
				maxDiffIdx = j - start
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		t.Logf("  Block %d [%4d:%4d]: SNR=%.1f dB maxDiff=%.2e at offset %d",
			b, start, end, snr, maxDiff, maxDiffIdx)
	}

	// Show first 30 samples
	t.Logf("\nFirst 30 samples:")
	for i := 0; i < 30 && i < libSamples; i++ {
		t.Logf("  [%4d] lib=%.8f go=%.8f diff=%.2e",
			i, libPcm[i], goPcm[i], float64(goPcm[i])-float64(libPcm[i]))
	}

	// Also decode the NEXT frame to see error propagation
	if problemIdx+1 < len(packets) {
		t.Logf("\nNext frame (packet %d) - checking error propagation:", problemIdx+1)

		libPcm2, libSamples2 := libDec2.DecodeFloat(packets[problemIdx+1], 5760)
		goPcm2, _ := decodeFloat32(goDec2, packets[problemIdx+1])

		// First 30 samples of next frame
		t.Logf("First 30 samples of next frame:")
		for i := 0; i < 30 && i < libSamples2; i++ {
			t.Logf("  [%4d] lib=%.8f go=%.8f diff=%.2e",
				i, libPcm2[i], goPcm2[i], float64(goPcm2[i])-float64(libPcm2[i]))
		}
	}
}
