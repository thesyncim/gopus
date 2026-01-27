// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkPacket15Debug investigates the systematic error in packet 15.
func TestSilkPacket15Debug(t *testing.T) {
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
	delay := 5

	// Fresh decoder
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()
	goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDec == nil {
		t.Fatal("Could not create libopus decoder")
	}
	libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
	libDec.Destroy()

	t.Logf("gopus samples: %d, libopus samples: %d", len(goNative), libSamples)

	// Analyze frame by frame (3 internal 20ms frames in 60ms packet)
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
		maxDiff := float32(0)
		maxDiffIdx := 0
		for i := start; i < end; i++ {
			goVal := goNative[i]
			libVal := libPcm[i+delay]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff == 0 {
				exactMatches++
			}
			if diff > maxDiff || -diff > maxDiff {
				if diff > 0 {
					maxDiff = diff
				} else {
					maxDiff = -diff
				}
				maxDiffIdx = i
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("  Frame %d [%3d-%3d]: SNR=%.1f dB, exact=%d/%d, maxDiff=%.0f at %d",
			frame, start, end-1, snr, exactMatches, end-start, maxDiff*32768, maxDiffIdx)
	}

	// Show samples around the maximum difference
	t.Log("\nShowing first 40 samples (subframe 0) with alignment:")
	for i := 0; i < 40 && i < len(goNative) && i+delay < libSamples; i++ {
		goVal := goNative[i]
		libVal := libPcm[i+delay]
		goInt := int(goVal * 32768)
		libInt := int(libVal * 32768)
		diff := goInt - libInt
		t.Logf("  [%2d] go=%6d lib=%6d diff=%5d", i, goInt, libInt, diff)
	}

	// Show middle frame samples
	t.Log("\nShowing samples 320-359 (frame 2, subframe 0):")
	for i := 320; i < 360 && i < len(goNative) && i+delay < libSamples; i++ {
		goVal := goNative[i]
		libVal := libPcm[i+delay]
		goInt := int(goVal * 32768)
		libInt := int(libVal * 32768)
		diff := goInt - libInt
		t.Logf("  [%3d] go=%6d lib=%6d diff=%5d", i, goInt, libInt, diff)
	}
}
