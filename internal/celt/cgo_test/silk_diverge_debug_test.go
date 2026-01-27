// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestPacket4FrameByFrame examines packet 4 frame by frame.
func TestPacket4FrameByFrame(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	pkt := packets[4]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 4: TOC=0x%02X, Mode=%d, Bandwidth=%d, FrameSize=%d",
		pkt[0], toc.Mode, toc.Bandwidth, toc.FrameSize)

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	config := silk.GetBandwidthConfig(silkBW)

	// Frame duration: 60ms = 3 x 20ms internal frames = 480 samples at 8kHz
	t.Logf("Duration=%dms, SampleRate=%dHz", duration, config.SampleRate)

	// Decode with gopus - fresh decoder
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()
	goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	// Decode with libopus - fresh decoder
	libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDec == nil {
		t.Fatal("Could not create libopus decoder")
	}
	libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
	libDec.Destroy()

	delay := 5 // 8kHz delay
	t.Logf("gopus samples: %d, libopus samples: %d, delay: %d", len(goNative), libSamples, delay)

	// Analyze frame by frame (3 internal 20ms frames in 60ms packet)
	// Each frame is 160 samples at 8kHz
	t.Log("\nFrame-by-frame analysis (160 samples per frame at 8kHz):")
	for frame := 0; frame < 3; frame++ {
		start := frame * 160
		end := start + 160
		if end > len(goNative) {
			end = len(goNative)
		}
		if start+delay+160 > libSamples {
			continue
		}

		var sumSqErr, sumSqSig float64
		exactMatches := 0
		firstDiffIdx := -1
		for i := start; i < end; i++ {
			goVal := goNative[i]
			libVal := libPcm[i+delay]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff == 0 {
				exactMatches++
			} else if firstDiffIdx < 0 {
				firstDiffIdx = i
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("  Frame %d [%3d-%3d]: SNR=%.1f dB, exact=%d/%d, firstDiff@%d",
			frame, start, end-1, snr, exactMatches, end-start, firstDiffIdx)
	}

	// Show samples around first divergence point (frame 2, around sample 320)
	t.Log("\nSamples around frame 2 start (samples 318-340):")
	for i := 318; i < 340 && i < len(goNative) && i+delay < libSamples; i++ {
		goVal := goNative[i]
		libVal := libPcm[i+delay]
		goInt := int(goVal * 32768)
		libInt := int(libVal * 32768)
		diff := goInt - libInt
		marker := ""
		if i == 320 {
			marker = " <-- frame 2 start"
		}
		t.Logf("  [%3d] go=%6d lib=%6d diff=%5d%s", i, goInt, libInt, diff, marker)
	}
}

// TestPacket15FrameByFrame examines packet 15 frame by frame.
func TestPacket15FrameByFrame(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil || len(packets) < 16 {
		t.Skip("Could not load packets")
	}

	pkt := packets[15]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 15: TOC=0x%02X, Mode=%d, Bandwidth=%d, FrameSize=%d",
		pkt[0], toc.Mode, toc.Bandwidth, toc.FrameSize)

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	config := silk.GetBandwidthConfig(silkBW)

	// Decode with gopus - fresh decoder
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()
	goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	// Decode with libopus - fresh decoder
	libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDec == nil {
		t.Fatal("Could not create libopus decoder")
	}
	libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
	libDec.Destroy()

	delay := 5
	t.Logf("gopus samples: %d, libopus samples: %d, delay: %d", len(goNative), libSamples, delay)

	// Analyze frame by frame
	t.Log("\nFrame-by-frame analysis:")
	for frame := 0; frame < 3; frame++ {
		start := frame * 160
		end := start + 160
		if end > len(goNative) {
			end = len(goNative)
		}
		if start+delay+160 > libSamples {
			continue
		}

		var sumSqErr, sumSqSig float64
		exactMatches := 0
		firstDiffIdx := -1
		for i := start; i < end; i++ {
			goVal := goNative[i]
			libVal := libPcm[i+delay]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff == 0 {
				exactMatches++
			} else if firstDiffIdx < 0 {
				firstDiffIdx = i
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("  Frame %d [%3d-%3d]: SNR=%.1f dB, exact=%d/%d, firstDiff@%d",
			frame, start, end-1, snr, exactMatches, end-start, firstDiffIdx)
	}

	// Show samples around divergence (frame 1, around sample 160)
	t.Log("\nSamples around frame 1 start (samples 158-180):")
	for i := 158; i < 180 && i < len(goNative) && i+delay < libSamples; i++ {
		goVal := goNative[i]
		libVal := libPcm[i+delay]
		goInt := int(goVal * 32768)
		libInt := int(libVal * 32768)
		diff := goInt - libInt
		marker := ""
		if i == 160 {
			marker = " <-- frame 1 start"
		}
		t.Logf("  [%3d] go=%6d lib=%6d diff=%5d%s", i, goInt, libInt, diff, marker)
	}
}

// TestCompareVoicedPacketDecodeParams compares parameters between bit-exact and divergent voiced packets.
func TestCompareVoicedPacketDecodeParams(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil {
		t.Skip("Could not load packets")
	}

	voicedPackets := []int{4, 5, 11, 12, 13, 14, 15, 19}
	divergentPackets := map[int]bool{4: true, 5: true, 15: true}

	t.Log("Comparing voiced packets (divergent marked with *):")

	for _, pktIdx := range voicedPackets {
		if pktIdx >= len(packets) {
			continue
		}
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		status := ""
		if divergentPackets[pktIdx] {
			status = "*DIVERGENT*"
		} else {
			status = "bit-exact"
		}

		t.Logf("  Packet %2d: %s, bandwidth=%d, duration=%dms",
			pktIdx, status, silkBW, duration)
	}
}
