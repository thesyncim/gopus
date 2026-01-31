// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilk48kHzComparison compares 48kHz output between Go and libopus.
// This test uses persistent decoders at 48kHz to match the compliance test setup.
func TestSilk48kHzComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create persistent decoders at 48kHz
	goDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}

	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Fatal("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("Testing 48kHz output with persistent decoders:")

	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			t.Logf("Packet %2d: SKIP (not SILK)", pktIdx)
			continue
		}

		// Decode with Go at 48kHz
		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
			t.Fatalf("Go decode packet %d failed: %v", pktIdx, err)
		}

		// Decode with libopus at 48kHz
		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libSamples < 0 {
			t.Fatalf("libopus decode packet %d failed", pktIdx)
		}

		// Compare lengths
		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		// Calculate SNR
		var sumSqErr, sumSqSig float64
		exactMatches := 0
		for i := 0; i < minLen; i++ {
			goVal := goSamples[i]
			libVal := libPcm[i]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff == 0 || (diff > -0.000001 && diff < 0.000001) {
				exactMatches++
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		status := "PASS"
		if snr < 40.0 {
			status = "FAIL"
		}

		exactPct := 0.0
		if minLen > 0 {
			exactPct = 100.0 * float64(exactMatches) / float64(minLen)
		}

		t.Logf("Packet %2d: go=%d lib=%d SNR=%6.1f dB, near-exact=%5.1f%% %s",
			pktIdx, len(goSamples), libSamples, snr, exactPct, status)

		// If SNR is low, show first few differences
		if snr < 40.0 && minLen > 0 {
			diffCount := 0
			for i := 0; i < minLen && diffCount < 10; i++ {
				goVal := goSamples[i]
				libVal := libPcm[i]
				diff := goVal - libVal
				if diff != 0 && (diff < -0.000001 || diff > 0.000001) {
					t.Logf("  Sample %d: go=%.6f, lib=%.6f, diff=%.6f", i, goVal, libVal, diff)
					diffCount++
				}
			}
		}
	}
}

// TestSilkDecodeVsDecodeFrame compares silk.Decode() vs silk.DecodeFrame().
// This isolates whether the resampling/sMid buffering layer is the issue.
func TestSilkDecodeVsDecodeFrame(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 5)
	if err != nil {
		t.Skip("Could not load packets")
	}

	config := silk.GetBandwidthConfig(silk.BandwidthNarrowband)

	// Create fresh decoder
	dec := silk.NewDecoder()

	t.Log("Comparing silk.Decode() output with libopus at 48kHz:")

	// Create libopus decoder at 48kHz for comparison
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		frameSizeSamples := toc.FrameSize

		// Use silk.Decode() which includes resampling
		goSamples, err := dec.Decode(pkt[1:], silkBW, frameSizeSamples, true)
		if err != nil {
			t.Fatalf("Go decode packet %d failed: %v", pktIdx, err)
		}

		// Decode with libopus at 48kHz
		libPcm, libSamples := libDec.DecodeFloat(pkt, frameSizeSamples*2)
		if libSamples < 0 {
			t.Fatalf("libopus decode packet %d failed", pktIdx)
		}

		// Compare lengths
		t.Logf("Packet %d: go=%d samples (expected %d), lib=%d samples",
			pktIdx, len(goSamples), frameSizeSamples, libSamples)

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		// Calculate SNR
		var sumSqErr, sumSqSig float64
		for i := 0; i < minLen; i++ {
			goVal := goSamples[i]
			libVal := libPcm[i]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		status := "PASS"
		if snr < 40.0 {
			status = "FAIL"
		}

		t.Logf("  SNR=%6.1f dB %s", snr, status)

		// Try with delay alignment (libopus might have delay from resampler)
		for delay := 0; delay <= 40; delay++ {
			if delay+minLen > libSamples {
				break
			}
			var sumSqErrDelayed float64
			var sumSqSigDelayed float64
			for i := 0; i < minLen-delay; i++ {
				goVal := goSamples[i]
				libVal := libPcm[i+delay]
				diff := goVal - libVal
				sumSqErrDelayed += float64(diff * diff)
				sumSqSigDelayed += float64(libVal * libVal)
			}
			snrDelayed := 10 * math.Log10(sumSqSigDelayed/sumSqErrDelayed)
			if math.IsNaN(snrDelayed) || math.IsInf(snrDelayed, 1) {
				snrDelayed = 999.0
			}
			if snrDelayed > snr+10 || snrDelayed > 60 {
				t.Logf("  Better with delay=%d: SNR=%6.1f dB", delay, snrDelayed)
			}
			if snrDelayed > 100 {
				break
			}
		}
	}

	// Also test with native rate decoder for comparison
	t.Log("\nComparing at native rate (8kHz):")
	dec.Reset()
	libDecNative, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDecNative == nil {
		t.Skip("Could not create native libopus decoder")
	}
	defer libDecNative.Destroy()

	delay := 5 // 8kHz delay
	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Use silk.DecodeFrame() which outputs native rate
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := dec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("Go decode packet %d failed: %v", pktIdx, err)
		}

		framesPerPacket := int(duration) / 20
		if framesPerPacket < 1 {
			framesPerPacket = 1
		}
		nativeFrameSize := framesPerPacket * 160
		libNative, libSamples := libDecNative.DecodeFloat(pkt, nativeFrameSize)
		if libSamples < 0 {
			continue
		}

		alignedLen := len(goNative)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}

		var sumSqErr, sumSqSig float64
		for i := 0; i < alignedLen; i++ {
			diff := goNative[i] - libNative[i+delay]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libNative[i+delay] * libNative[i+delay])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("Packet %d native: SNR=%6.1f dB", pktIdx, snr)
	}
}
