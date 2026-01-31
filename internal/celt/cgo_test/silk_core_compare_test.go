// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestSilkCoreCompare compares SILK decode at core level
// to identify exactly where divergence occurs.
func TestSilkCoreCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadSilkCorePackets(t, bitFile, 5)
	if len(packets) < 2 {
		t.Skip("Could not load enough test packets")
	}

	channels := 1

	// Create decoders
	goDec, _ := gopus.NewDecoderDefault(48000, channels)
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode packet 0 to initialize state
	goDec.DecodeFloat32(packets[0])
	libDec.DecodeFloat(packets[0], 5760)

	// Now focus on packet 1 where divergence starts
	pkt := packets[1]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 1: %d bytes, mode=%v, frameSize=%d, bandwidth=%d",
		len(pkt), toc.Mode, toc.FrameSize, toc.Bandwidth)

	// Decode with gopus
	goPcm, goErr := goDec.DecodeFloat32(pkt)
	if goErr != nil {
		t.Fatalf("gopus decode failed: %v", goErr)
	}

	// Decode with libopus
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	// Find first divergence point
	minLen := len(goPcm)
	if libSamples < minLen {
		minLen = libSamples
	}

	firstDiff := -1
	var firstDiffValue float32
	for i := 0; i < minLen; i++ {
		diff := goPcm[i] - libPcm[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > 0.0001 {
			firstDiff = i
			firstDiffValue = diff
			break
		}
	}

	if firstDiff < 0 {
		t.Log("No divergence found in packet 1!")
		return
	}

	t.Logf("First divergence at sample %d (diff=%.6f)", firstDiff, firstDiffValue)

	// Show context around divergence
	start := firstDiff - 10
	if start < 0 {
		start = 0
	}
	end := firstDiff + 20
	if end > minLen {
		end = minLen
	}

	t.Log("Samples around divergence:")
	for i := start; i < end; i++ {
		diff := goPcm[i] - libPcm[i]
		marker := ""
		if i == firstDiff {
			marker = " <-- FIRST DIFF"
		}
		t.Logf("  [%d] go=%.6f lib=%.6f diff=%.6f%s", i, goPcm[i], libPcm[i], diff, marker)
	}

	// Calculate which internal frame and subframe the divergence is in
	// For 60ms SILK: 3 internal frames of 20ms = 960 samples each at 48kHz
	internalFrame := firstDiff / 960
	sampleInFrame := firstDiff % 960

	// At 48kHz, each 5ms subframe is 240 samples
	subframe := sampleInFrame / 240
	sampleInSubframe := sampleInFrame % 240

	t.Logf("\nDivergence location:")
	t.Logf("  48kHz sample: %d", firstDiff)
	t.Logf("  Internal frame: %d (of 3)", internalFrame)
	t.Logf("  Subframe: %d (of 4)", subframe)
	t.Logf("  Sample in subframe: %d", sampleInSubframe)

	// The resampler ratio is 48kHz/native. For WB (16kHz) it's 3x.
	// So native sample would be around firstDiff/3
	nativeSample := firstDiff / 3
	nativeFrame := nativeSample / 320           // 20ms at 16kHz = 320 samples
	nativeSubframe := (nativeSample % 320) / 80 // 5ms at 16kHz = 80 samples
	nativeSampleInSubframe := nativeSample % 80

	t.Logf("\nApproximate native (16kHz) location:")
	t.Logf("  Native sample: ~%d", nativeSample)
	t.Logf("  Native frame: ~%d", nativeFrame)
	t.Logf("  Native subframe: ~%d", nativeSubframe)
	t.Logf("  Sample in subframe: ~%d", nativeSampleInSubframe)
}

// TestSilkPacket0vs1 compares packet 0 (which works) vs packet 1 (which diverges)
func TestSilkPacket0vs1(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadSilkCorePackets(t, bitFile, 5)
	if len(packets) < 2 {
		t.Skip("Could not load enough test packets")
	}

	channels := 1

	for pktIdx := 0; pktIdx < 2; pktIdx++ {
		t.Logf("\n=== Packet %d ===", pktIdx)

		// Fresh decoders for each packet
		goDec, _ := gopus.NewDecoderDefault(48000, channels)
		libDec, _ := NewLibopusDecoder(48000, channels)
		defer libDec.Destroy()

		// If testing packet 1, decode packet 0 first
		if pktIdx == 1 {
			goDec.DecodeFloat32(packets[0])
			libDec.DecodeFloat(packets[0], 5760)
		}

		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, _ := goDec.DecodeFloat32(pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		// Calculate SNR per internal frame (960 samples at 48kHz)
		framesPerPacket := 1
		if toc.FrameSize == 1920 {
			framesPerPacket = 2
		} else if toc.FrameSize == 2880 {
			framesPerPacket = 3
		}

		samplesPerFrame := toc.FrameSize / framesPerPacket
		t.Logf("Packet has %d internal frames of %d samples each", framesPerPacket, samplesPerFrame)

		for frame := 0; frame < framesPerPacket; frame++ {
			startSample := frame * samplesPerFrame
			endSample := startSample + samplesPerFrame
			if endSample > len(goPcm) || endSample > libSamples {
				break
			}

			var sigE, noiseE float64
			for i := startSample; i < endSample; i++ {
				sig := float64(libPcm[i])
				noise := float64(goPcm[i]) - sig
				sigE += sig * sig
				noiseE += noise * noise
			}

			snr := 999.0
			if sigE > 0 && noiseE > 0 {
				snr = 10 * coreLog10(sigE/noiseE)
			}

			// Find first divergence in this frame
			firstDiff := -1
			for i := startSample; i < endSample; i++ {
				diff := goPcm[i] - libPcm[i]
				if diff < 0 {
					diff = -diff
				}
				if diff > 0.0001 {
					firstDiff = i - startSample
					break
				}
			}

			t.Logf("  Frame %d [%d-%d]: SNR=%.1f dB, first_diff_at=%d",
				frame, startSample, endSample-1, snr, firstDiff)
		}
	}
}

// loadSilkCorePackets loads packets for core comparison tests
func loadSilkCorePackets(t *testing.T, bitFile string, maxPackets int) [][]byte {
	t.Helper()

	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Logf("Cannot read %s: %v", bitFile, err)
		return nil
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		if maxPackets > 0 && len(packets) >= maxPackets {
			break
		}
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:])
		offset += 4

		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}

		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	return packets
}

func coreLog10(x float64) float64 {
	return 2.302585092994046 * coreLn(x) / 2.302585092994046
}

func coreLn(x float64) float64 {
	// Simple natural log approximation
	if x <= 0 {
		return -999
	}
	n := 0
	for x >= 2 {
		x /= 2
		n++
	}
	for x < 1 {
		x *= 2
		n--
	}
	// x is now in [1, 2)
	y := (x - 1) / (x + 1)
	y2 := y * y
	result := 2 * y * (1 + y2/3 + y2*y2/5 + y2*y2*y2/7)
	return result + float64(n)*0.693147180559945
}
