// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestPacket15SubframeTrace traces each subframe of packet 15.
func TestPacket15SubframeTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil || len(packets) < 16 {
		t.Skip("Could not load packets")
	}

	pkt := packets[15]
	toc := gopus.ParseTOC(pkt[0])

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
	t.Logf("Packet 15: gopus samples=%d, libopus samples=%d", len(goNative), libSamples)

	// For 60ms packet at 8kHz: 3 frames x 160 samples = 480 samples
	// Each frame has 4 subframes of 40 samples
	subfrLength := 40
	nbSubfr := 4

	t.Log("\nSubframe-by-subframe analysis:")
	for frame := 0; frame < 3; frame++ {
		frameStart := frame * 160
		t.Logf("\n=== Frame %d (samples %d-%d) ===", frame, frameStart, frameStart+159)

		for sf := 0; sf < nbSubfr; sf++ {
			sfStart := frameStart + sf*subfrLength
			sfEnd := sfStart + subfrLength

			var sumSqErr, sumSqSig float64
			exactMatches := 0
			maxDiff := float32(0)
			maxDiffIdx := 0

			for i := sfStart; i < sfEnd && i < len(goNative) && i+delay < libSamples; i++ {
				goVal := goNative[i]
				libVal := libPcm[i+delay]
				diff := goVal - libVal
				sumSqErr += float64(diff * diff)
				sumSqSig += float64(libVal * libVal)
				if diff == 0 {
					exactMatches++
				}
				absDiff := diff
				if absDiff < 0 {
					absDiff = -diff
				}
				if absDiff > maxDiff {
					maxDiff = absDiff
					maxDiffIdx = i
				}
			}

			snr := 10 * math.Log10(sumSqSig/sumSqErr)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999.0
			}

			status := "OK"
			if snr < 999 {
				status = "DIFF"
			}

			t.Logf("  Subframe %d [%3d-%3d]: SNR=%6.1f dB, exact=%2d/%2d, maxDiff=%5.0f@%d %s",
				sf, sfStart, sfEnd-1, snr, exactMatches, subfrLength, maxDiff*32768, maxDiffIdx, status)

			// Show first few samples if there's divergence
			if snr < 999 && sf*subfrLength+frameStart >= 160 {
				t.Log("    First 10 samples:")
				for i := sfStart; i < sfStart+10 && i < len(goNative) && i+delay < libSamples; i++ {
					goVal := goNative[i]
					libVal := libPcm[i+delay]
					goInt := int(goVal * 32768)
					libInt := int(libVal * 32768)
					diff := goInt - libInt
					t.Logf("      [%3d] go=%6d lib=%6d diff=%5d", i, goInt, libInt, diff)
				}
			}
		}
	}
}

// TestPacket4SubframeTrace traces each subframe of packet 4.
func TestPacket4SubframeTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	pkt := packets[4]
	toc := gopus.ParseTOC(pkt[0])

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
	t.Logf("Packet 4: gopus samples=%d, libopus samples=%d", len(goNative), libSamples)

	subfrLength := 40
	nbSubfr := 4

	t.Log("\nSubframe-by-subframe analysis:")
	for frame := 0; frame < 3; frame++ {
		frameStart := frame * 160
		t.Logf("\n=== Frame %d (samples %d-%d) ===", frame, frameStart, frameStart+159)

		for sf := 0; sf < nbSubfr; sf++ {
			sfStart := frameStart + sf*subfrLength
			sfEnd := sfStart + subfrLength

			var sumSqErr, sumSqSig float64
			exactMatches := 0
			maxDiff := float32(0)
			maxDiffIdx := 0

			for i := sfStart; i < sfEnd && i < len(goNative) && i+delay < libSamples; i++ {
				goVal := goNative[i]
				libVal := libPcm[i+delay]
				diff := goVal - libVal
				sumSqErr += float64(diff * diff)
				sumSqSig += float64(libVal * libVal)
				if diff == 0 {
					exactMatches++
				}
				absDiff := diff
				if absDiff < 0 {
					absDiff = -diff
				}
				if absDiff > maxDiff {
					maxDiff = absDiff
					maxDiffIdx = i
				}
			}

			snr := 10 * math.Log10(sumSqSig/sumSqErr)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999.0
			}

			status := "OK"
			if snr < 999 {
				status = "DIFF"
			}

			t.Logf("  Subframe %d [%3d-%3d]: SNR=%6.1f dB, exact=%2d/%2d, maxDiff=%5.0f@%d %s",
				sf, sfStart, sfEnd-1, snr, exactMatches, subfrLength, maxDiff*32768, maxDiffIdx, status)
		}
	}
}
