//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for CELT decoding.
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestDecodePacketVsLibopus compares single packet decoding
func TestDecodePacketVsLibopus(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 20)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2

	t.Run("FirstFewPackets", func(t *testing.T) {
		for i, pkt := range packets[:5] {
			toc := gopus.ParseTOC(pkt[0])
			t.Logf("Packet %d: %d bytes, stereo=%v, frameSize=%d", i, len(pkt), toc.Stereo, toc.FrameSize)

			// Decode with libopus (fresh decoder each time)
			libDec, err := NewLibopusDecoder(48000, channels)
			if err != nil || libDec == nil {
				t.Fatalf("Failed to create libopus decoder")
			}
			libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
			libDec.Destroy()
			if libSamples < 0 {
				t.Errorf("libopus decode failed: %d", libSamples)
				continue
			}

			// Decode with gopus
			goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
			goPcm, decErr := decodeFloat32(goDec, pkt)
			if decErr != nil {
				t.Errorf("gopus decode failed: %v", decErr)
				continue
			}

			// Compare
			goSamples := len(goPcm) / channels
			if goSamples != libSamples {
				t.Errorf("Sample count mismatch: gopus=%d, libopus=%d", goSamples, libSamples)
				continue
			}

			// Calculate SNR
			var sigPow, noisePow float64
			for j := 0; j < goSamples*channels; j++ {
				sig := float64(libPcm[j])
				noise := float64(goPcm[j]) - sig
				sigPow += sig * sig
				noisePow += noise * noise
			}

			snr := 10 * math.Log10(sigPow/noisePow)
			if math.IsNaN(snr) || math.IsInf(snr, 0) {
				snr = 999
			}

			t.Logf("  Packet %d SNR: %.1f dB", i, snr)
			if snr < 60 {
				t.Errorf("  Poor SNR: %.1f dB (threshold: 60 dB)", snr)
			}
		}
	})
}

// TestDecodeDivergencePoint finds the exact packet where divergence starts
func TestDecodeDivergencePoint(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2

	// Create persistent decoders
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	firstBadPkt := -1
	for i, pkt := range packets {
		// Decode with gopus
		goPcm, decErr := decodeFloat32(goDec, pkt)
		if decErr != nil {
			t.Logf("Packet %d: gopus error: %v", i, decErr)
			continue
		}

		// Decode with libopus
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples < 0 {
			t.Logf("Packet %d: libopus error: %d", i, libSamples)
			continue
		}

		// Compare
		goSamples := len(goPcm) / channels
		if goSamples != libSamples {
			t.Logf("Packet %d: sample count mismatch gopus=%d libopus=%d", i, goSamples, libSamples)
			continue
		}

		// Calculate SNR
		var sigPow, noisePow float64
		var maxDiff float64
		for j := 0; j < goSamples*channels; j++ {
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

		if snr < 40 && firstBadPkt == -1 {
			firstBadPkt = i
			toc := gopus.ParseTOC(pkt[0])
			t.Logf("\n*** First divergence at packet %d ***", i)
			t.Logf("  TOC: stereo=%v, frameSize=%d, mode=%v", toc.Stereo, toc.FrameSize, toc.Mode)
			t.Logf("  SNR: %.1f dB, MaxDiff: %.6f", snr, maxDiff)

			// Show surrounding packets for context
			for j := maxInt(0, i-3); j <= minInt(len(packets)-1, i+3); j++ {
				pkt2 := packets[j]
				toc2 := gopus.ParseTOC(pkt2[0])
				marker := ""
				if j == i {
					marker = " <-- DIVERGES"
				}
				t.Logf("  Pkt %d: stereo=%v frame=%d len=%d%s", j, toc2.Stereo, toc2.FrameSize, len(pkt2), marker)
			}
			break
		}

		// Log progress every 500 packets
		if i%500 == 0 && i > 0 {
			t.Logf("Processed %d packets, SNR still good (>40 dB)", i)
		}
	}

	if firstBadPkt == -1 {
		t.Log("No divergence found! All packets decode correctly.")
	} else {
		t.Logf("\nDivergence starts at packet %d", firstBadPkt)
	}
}

// TestDecodeAllPacketsSNR calculates SNR for the entire stream
func TestDecodeAllPacketsSNR(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2

	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	var totalSig, totalNoise float64
	var maxDiff float64
	maxDiffPkt := 0
	lowSNRPackets := 0

	for i, pkt := range packets {
		goPcm, decErr := decodeFloat32(goDec, pkt)
		if decErr != nil {
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples < 0 {
			continue
		}

		goSamples := len(goPcm) / channels
		if goSamples != libSamples {
			continue
		}

		var pktSig, pktNoise float64
		var pktMaxDiff float64
		for j := 0; j < goSamples*channels; j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			pktSig += sig * sig
			pktNoise += noise * noise
			if math.Abs(noise) > pktMaxDiff {
				pktMaxDiff = math.Abs(noise)
			}
		}

		totalSig += pktSig
		totalNoise += pktNoise

		if pktMaxDiff > maxDiff {
			maxDiff = pktMaxDiff
			maxDiffPkt = i
		}

		pktSNR := 10 * math.Log10(pktSig/pktNoise)
		if pktSNR < 40 {
			lowSNRPackets++
		}
	}

	overallSNR := 10 * math.Log10(totalSig/totalNoise)
	t.Logf("Overall SNR: %.2f dB", overallSNR)
	t.Logf("Max difference: %.6f at packet %d", maxDiff, maxDiffPkt)
	t.Logf("Packets with SNR < 40 dB: %d / %d (%.1f%%)",
		lowSNRPackets, len(packets), 100*float64(lowSNRPackets)/float64(len(packets)))

	// Show packet info for max diff
	if maxDiffPkt < len(packets) {
		toc := gopus.ParseTOC(packets[maxDiffPkt][0])
		t.Logf("Max diff packet: stereo=%v, frameSize=%d", toc.Stereo, toc.FrameSize)
	}
}

func loadPackets(t *testing.T, bitFile string, maxPackets int) [][]byte {
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

// TestAnalyzeSNRByFrameSize breaks down SNR by frame size and stereo flag
func TestAnalyzeSNRByFrameSize(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2

	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	type key struct {
		frameSize int
		stereo    bool
	}
	type stats struct {
		count       int
		totalSig    float64
		totalNoise  float64
		lowSNRCount int
	}
	results := make(map[key]*stats)

	for _, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		k := key{toc.FrameSize, toc.Stereo}

		if results[k] == nil {
			results[k] = &stats{}
		}

		goPcm, decErr := decodeFloat32(goDec, pkt)
		if decErr != nil {
			continue
		}
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples < 0 {
			continue
		}

		goSamples := len(goPcm) / channels
		if goSamples != libSamples {
			continue
		}

		var sig, noise float64
		for j := 0; j < goSamples*channels; j++ {
			s := float64(libPcm[j])
			n := float64(goPcm[j]) - s
			sig += s * s
			noise += n * n
		}

		results[k].count++
		results[k].totalSig += sig
		results[k].totalNoise += noise
		snr := 10 * math.Log10(sig/noise)
		if snr < 40 {
			results[k].lowSNRCount++
		}
	}

	t.Log("\nSNR Analysis by Frame Size:")
	t.Log("Frame Size | Stereo | Count | Avg SNR (dB) | Low SNR %")
	t.Log("-----------|--------|-------|--------------|----------")
	for k, v := range results {
		avgSNR := 10 * math.Log10(v.totalSig/v.totalNoise)
		lowPct := 100 * float64(v.lowSNRCount) / float64(v.count)
		stereoStr := "mono"
		if k.stereo {
			stereoStr = "STEREO"
		}
		t.Logf("%10d | %-6s | %5d | %12.1f | %8.1f%%",
			k.frameSize, stereoStr, v.count, avgSNR, lowPct)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TestCheckTransientInBadPackets checks if bad packets are transient frames
func TestCheckTransientInBadPackets(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	// Check transient flag for bad mono packets
	badMonoPackets := []int{4, 5, 18, 19, 31, 32, 33, 34, 35}

	t.Log("Checking transient flag for bad mono packets:")
	for _, idx := range badMonoPackets {
		if idx >= len(packets) {
			continue
		}
		pkt := packets[idx]
		toc := gopus.ParseTOC(pkt[0])

		// The transient flag is in the CELT frame data after silence/postfilter flags
		// For a quick check, let's just log what we know
		t.Logf("Packet %d: frameSize=%d, len=%d bytes", idx, toc.FrameSize, len(pkt))

		// Look at first few bytes to see frame structure
		if len(pkt) > 1 {
			t.Logf("  First bytes: %02x %02x %02x %02x", pkt[0], pkt[1],
				func() byte {
					if len(pkt) > 2 {
						return pkt[2]
					}
					return 0
				}(),
				func() byte {
					if len(pkt) > 3 {
						return pkt[3]
					}
					return 0
				}())
		}
	}
}

// TestAnalyzeBadMonoPacket examines a bad mono packet (packet 31)
func TestAnalyzeBadMonoPacket(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2
	targetPacket := 31 // Very bad mono packet (4.8 dB SNR)

	// Create fresh decoders and decode up to target
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode all packets up to the target
	for i := 0; i < targetPacket; i++ {
		decodeFloat32(goDec, packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet %d: %d bytes, stereo=%v, frameSize=%d, mode=%v",
		targetPacket, len(pkt), toc.Stereo, toc.FrameSize, toc.Mode)

	goPcm, decErr := decodeFloat32(goDec, pkt)
	if decErr != nil {
		t.Fatalf("gopus decode failed: %v", decErr)
	}

	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	goSamples := len(goPcm) / channels

	// Find max diff and first significant diff
	var maxDiff float64
	maxDiffIdx := 0
	firstDiffIdx := -1
	const threshold = 0.001

	for i := 0; i < goSamples*channels; i++ {
		diff := math.Abs(float64(goPcm[i]) - float64(libPcm[i]))
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
		if diff > threshold && firstDiffIdx == -1 {
			firstDiffIdx = i
		}
	}

	t.Logf("First significant diff at index %d", firstDiffIdx)
	t.Logf("Max diff: %.6f at index %d", maxDiff, maxDiffIdx)

	// Show samples around first diff
	if firstDiffIdx >= 0 {
		t.Log("\nSamples around first diff:")
		start := maxInt(0, firstDiffIdx-4)
		end := minInt(goSamples*channels, firstDiffIdx+16)
		t.Log("Index\tgopus\t\tlibopus\t\tdiff")
		for i := start; i < end; i++ {
			marker := ""
			if i == firstDiffIdx {
				marker = " <-- FIRST"
			}
			t.Logf("%d\t%.6f\t%.6f\t%.6f%s", i, goPcm[i], libPcm[i], goPcm[i]-libPcm[i], marker)
		}
	}

	// Calculate SNR
	var sigPow, noisePow float64
	for i := 0; i < goSamples*channels; i++ {
		sig := float64(libPcm[i])
		noise := float64(goPcm[i]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}
	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("\nPacket %d SNR: %.1f dB", targetPacket, snr)
}

// TestAnalyzeFirstPacket examines the very first packet to understand initial divergence
func TestAnalyzeFirstPacket(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2

	// Create fresh decoders for packet 0
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 0: %d bytes, stereo=%v, frameSize=%d, mode=%v",
		len(pkt), toc.Stereo, toc.FrameSize, toc.Mode)

	goPcm, decErr := decodeFloat32(goDec, pkt)
	if decErr != nil {
		t.Fatalf("gopus decode failed: %v", decErr)
	}

	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	goSamples := len(goPcm) / channels

	// Find max diff location
	var maxDiff float64
	maxDiffIdx := 0
	for i := 0; i < goSamples*channels; i++ {
		diff := math.Abs(float64(goPcm[i]) - float64(libPcm[i]))
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}

	t.Logf("Max diff: %.6f at index %d", maxDiff, maxDiffIdx)

	// Show first 40 samples (mono duplicated to stereo)
	t.Log("\nFirst 40 output samples (L/R interleaved):")
	t.Log("Index\tgopus\t\tlibopus\t\tdiff")
	for i := 0; i < minInt(40, goSamples*channels); i++ {
		diff := goPcm[i] - libPcm[i]
		marker := ""
		if math.Abs(float64(diff)) > 0.001 {
			marker = " *"
		}
		t.Logf("%d\t%.6f\t%.6f\t%.6f%s", i, goPcm[i], libPcm[i], diff, marker)
	}

	// Calculate SNR
	var sigPow, noisePow float64
	for i := 0; i < goSamples*channels; i++ {
		sig := float64(libPcm[i])
		noise := float64(goPcm[i]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}
	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("\nPacket 0 SNR: %.1f dB", snr)
}

// TestAnalyzeWorstPacket examines the packet with maximum divergence in detail
func TestAnalyzeWorstPacket(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2
	worstPacket := 3287 // From previous analysis

	if worstPacket >= len(packets) {
		t.Skipf("Worst packet %d not available (only %d packets)", worstPacket, len(packets))
	}

	// Decode all packets up to the worst one to establish correct state
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	for i := 0; i < worstPacket; i++ {
		decodeFloat32(goDec, packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode the worst packet
	pkt := packets[worstPacket]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet %d: %d bytes, stereo=%v, frameSize=%d, mode=%v",
		worstPacket, len(pkt), toc.Stereo, toc.FrameSize, toc.Mode)

	goPcm, decErr := decodeFloat32(goDec, pkt)
	if decErr != nil {
		t.Fatalf("gopus decode failed: %v", decErr)
	}

	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	goSamples := len(goPcm) / channels
	t.Logf("gopus samples: %d, libopus samples: %d", goSamples, libSamples)

	if goSamples != libSamples {
		t.Errorf("Sample count mismatch")
		return
	}

	// Find where divergence starts (first sample with significant difference)
	const threshold = 0.001
	firstDivergence := -1
	var maxDiff float64
	maxDiffIdx := 0

	for i := 0; i < goSamples*channels; i++ {
		diff := math.Abs(float64(goPcm[i]) - float64(libPcm[i]))
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
		if diff > threshold && firstDivergence == -1 {
			firstDivergence = i
		}
	}

	t.Logf("First divergence at sample index %d (sample %d, ch %d)",
		firstDivergence, firstDivergence/channels, firstDivergence%channels)
	t.Logf("Max diff: %.6f at index %d (sample %d, ch %d)",
		maxDiff, maxDiffIdx, maxDiffIdx/channels, maxDiffIdx%channels)

	// Show samples around first divergence
	if firstDivergence >= 0 {
		start := maxInt(0, firstDivergence-4)
		end := minInt(goSamples*channels, firstDivergence+12)
		t.Log("\nSamples around first divergence:")
		t.Log("Index\tgopus\t\tlibopus\t\tdiff")
		for i := start; i < end; i++ {
			marker := ""
			if i == firstDivergence {
				marker = " <-- FIRST DIVERGENCE"
			}
			t.Logf("%d\t%.6f\t%.6f\t%.6f%s",
				i, goPcm[i], libPcm[i], goPcm[i]-libPcm[i], marker)
		}
	}

	// Show L/R separately for first few samples
	t.Log("\nFirst 20 samples L/R comparison:")
	t.Log("Sample\tL_go\t\tL_lib\t\tR_go\t\tR_lib")
	for i := 0; i < minInt(20, goSamples); i++ {
		lGo := goPcm[i*2]
		lLib := libPcm[i*2]
		rGo := goPcm[i*2+1]
		rLib := libPcm[i*2+1]
		marker := ""
		if math.Abs(float64(lGo-lLib)) > threshold || math.Abs(float64(rGo-rLib)) > threshold {
			marker = " *"
		}
		t.Logf("%d\t%.6f\t%.6f\t%.6f\t%.6f%s", i, lGo, lLib, rGo, rLib, marker)
	}

	// Calculate per-channel SNR
	var sigL, noiseL, sigR, noiseR float64
	for i := 0; i < goSamples; i++ {
		lGo := float64(goPcm[i*2])
		lLib := float64(libPcm[i*2])
		rGo := float64(goPcm[i*2+1])
		rLib := float64(libPcm[i*2+1])

		sigL += lLib * lLib
		noiseL += (lGo - lLib) * (lGo - lLib)
		sigR += rLib * rLib
		noiseR += (rGo - rLib) * (rGo - rLib)
	}

	snrL := 10 * math.Log10(sigL/noiseL)
	snrR := 10 * math.Log10(sigR/noiseR)
	t.Logf("\nPer-channel SNR: L=%.1f dB, R=%.1f dB", snrL, snrR)
}

// TestAnalyzeBadPacketPattern analyzes all bad packets to find patterns
func TestAnalyzeBadPacketPattern(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2

	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	// Categorize bad packets
	type badPacketInfo struct {
		idx       int
		snr       float64
		stereo    bool
		frameSize int
		pktLen    int
	}
	var badPackets []badPacketInfo

	for i, pkt := range packets {
		goPcm, decErr := decodeFloat32(goDec, pkt)
		if decErr != nil {
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples < 0 {
			continue
		}

		goSamples := len(goPcm) / channels
		if goSamples != libSamples {
			continue
		}

		var sigPow, noisePow float64
		for j := 0; j < goSamples*channels; j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		if snr < 40 {
			toc := gopus.ParseTOC(pkt[0])
			badPackets = append(badPackets, badPacketInfo{
				idx:       i,
				snr:       snr,
				stereo:    toc.Stereo,
				frameSize: toc.FrameSize,
				pktLen:    len(pkt),
			})
		}
	}

	// Summarize by category
	type category struct {
		stereo    bool
		frameSize int
	}
	counts := make(map[category]int)
	totals := make(map[category]int)

	for _, bp := range badPackets {
		cat := category{bp.stereo, bp.frameSize}
		counts[cat]++
	}

	// Count total packets by category
	for _, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		cat := category{toc.Stereo, toc.FrameSize}
		totals[cat]++
	}

	t.Logf("Bad packets breakdown (SNR < 40 dB):")
	t.Logf("Category\t\tBad/Total\tPercentage")
	for cat, bad := range counts {
		total := totals[cat]
		pct := 100.0 * float64(bad) / float64(total)
		stereoStr := "mono"
		if cat.stereo {
			stereoStr = "stereo"
		}
		t.Logf("%s %d:\t\t%d/%d\t\t%.1f%%", stereoStr, cat.frameSize, bad, total, pct)
	}

	// Show first few bad mono packets
	t.Log("\nFirst 10 bad mono packets:")
	count := 0
	for _, bp := range badPackets {
		if !bp.stereo && count < 10 {
			t.Logf("  Packet %d: SNR=%.1f dB, frameSize=%d, len=%d", bp.idx, bp.snr, bp.frameSize, bp.pktLen)
			count++
		}
	}
}

// TestFindFirstBadPacket finds the first packet where SNR drops significantly
func TestFindFirstBadPacket(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2

	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	// Track SNR over time
	const windowSize = 10
	recentSNRs := make([]float64, 0, windowSize)

	for i, pkt := range packets {
		goPcm, decErr := decodeFloat32(goDec, pkt)
		if decErr != nil {
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples < 0 {
			continue
		}

		goSamples := len(goPcm) / channels
		if goSamples != libSamples {
			continue
		}

		// Calculate SNR
		var sigPow, noisePow float64
		for j := 0; j < goSamples*channels; j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		recentSNRs = append(recentSNRs, snr)
		if len(recentSNRs) > windowSize {
			recentSNRs = recentSNRs[1:]
		}

		// Check for sudden drop
		if len(recentSNRs) == windowSize && snr < 30 {
			// Calculate average of previous packets
			var avgPrev float64
			for _, s := range recentSNRs[:windowSize-1] {
				avgPrev += s
			}
			avgPrev /= float64(windowSize - 1)

			if avgPrev > 50 && snr < 30 {
				toc := gopus.ParseTOC(pkt[0])
				t.Logf("\n*** SUDDEN DROP at packet %d ***", i)
				t.Logf("Previous avg SNR: %.1f dB, Current SNR: %.1f dB", avgPrev, snr)
				t.Logf("TOC: stereo=%v, frameSize=%d, mode=%v", toc.Stereo, toc.FrameSize, toc.Mode)
				t.Logf("Packet length: %d bytes", len(pkt))
				return
			}
		}

		// Log first 50 packets for context
		if i < 50 {
			toc := gopus.ParseTOC(pkt[0])
			t.Logf("Pkt %d: SNR=%.1f dB, stereo=%v, frame=%d", i, snr, toc.Stereo, toc.FrameSize)
		}
	}

	t.Log("No sudden SNR drop detected")
}
