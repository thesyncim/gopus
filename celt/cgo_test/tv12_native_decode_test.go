//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests native SILK decoding without resampling.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestTV12NativeDecodeOnly tests SILK DecodeFrame (native rate) vs libopus at native rate.
// This isolates whether the issue is in SILK decoding or resampling.
func TestTV12NativeDecodeOnly(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder (no resampling)
	silkDec := silk.NewDecoder()

	// Create libopus decoder at 8kHz (matches NB native rate)
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	if libDec8k == nil {
		t.Skip("Could not create 8k libopus decoder")
	}
	defer libDec8k.Destroy()

	// Create libopus decoder at 12kHz (matches MB native rate)
	libDec12k, _ := NewLibopusDecoder(12000, 1)
	if libDec12k == nil {
		t.Skip("Could not create 12k libopus decoder")
	}
	defer libDec12k.Destroy()

	t.Log("=== Native SILK decode comparison (DecodeFrame only, no resampling) ===")

	prevBW := silk.Bandwidth(255)

	for i := 0; i <= 828; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		// Choose libopus decoder based on bandwidth
		var libDec *LibopusDecoder
		var delay int
		if silkBW == silk.BandwidthNarrowband {
			libDec = libDec8k
			delay = 5
		} else {
			libDec = libDec12k
			delay = 10
		}

		// Decode with gopus DecodeFrame (native rate)
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("Packet %d: gopus error: %v", i, err)
			continue
		}

		// Decode with libopus at native rate
		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goNative)*2)
		if libSamples < 0 {
			continue
		}

		// Align and compare
		alignedLen := len(goNative)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}
		if alignedLen <= 0 {
			continue
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < alignedLen; j++ {
			diff := goNative[j] - libPcm[j+delay]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j+delay] * libPcm[j+delay])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Log bandwidth changes and failing packets
		isBWChange := silkBW != prevBW
		prevBW = silkBW

		if isBWChange || snr < 40 || (i >= 824 && i <= 828) {
			marker := ""
			if isBWChange {
				marker = " <-- BW CHANGE"
			}
			t.Logf("Packet %4d: BW=%v (%dkHz), NativeSNR=%6.1f dB%s",
				i, silkBW, config.SampleRate/1000, snr, marker)
		}
	}
}

// TestTV12NativeFreshVsStateful tests native SILK decoding with fresh vs stateful decoder.
func TestTV12NativeFreshVsStateful(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Log("=== Native SILK: Fresh vs Stateful for packet 826 ===")

	// Test 1: Fresh SILK decoder just for packet 826
	t.Log("\n1. Fresh SILK decoder for packet 826:")
	{
		silkDec := silk.NewDecoder()
		libDec, _ := NewLibopusDecoder(8000, 1)
		if libDec == nil {
			t.Skip("Could not create libopus decoder")
		}

		pkt826 := packets[826]
		toc := gopus.ParseTOC(pkt826[0])
		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		var rd rangecoding.Decoder
		rd.Init(pkt826[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("Decode error: %v", err)
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt826, len(goNative)*2)
		libDec.Destroy()

		delay := 5
		alignedLen := len(goNative)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < alignedLen; j++ {
			diff := goNative[j] - libPcm[j+delay]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j+delay] * libPcm[j+delay])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("   Fresh native SNR: %.1f dB", snr)
	}

	// Test 2: SILK decoder with state from packets 0-825
	t.Log("\n2. Stateful SILK decoder (processed 0-825):")
	{
		silkDec := silk.NewDecoder()

		// Also need libopus with accumulated state at 8k
		libDec8k, _ := NewLibopusDecoder(8000, 1)
		if libDec8k == nil {
			t.Skip("Could not create 8k libopus decoder")
		}
		libDec12k, _ := NewLibopusDecoder(12000, 1)
		if libDec12k == nil {
			libDec8k.Destroy()
			t.Skip("Could not create 12k libopus decoder")
		}

		// Build state
		for i := 0; i < 826; i++ {
			pkt := packets[i]
			toc := gopus.ParseTOC(pkt[0])
			if toc.Mode != gopus.ModeSILK {
				continue
			}

			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if !ok {
				continue
			}
			duration := silk.FrameDurationFromTOC(toc.FrameSize)

			var rd rangecoding.Decoder
			rd.Init(pkt[1:])
			silkDec.DecodeFrame(&rd, silkBW, duration, true)

			// Also decode with libopus
			if silkBW == silk.BandwidthNarrowband {
				libDec8k.DecodeFloat(pkt, 960)
			} else {
				libDec12k.DecodeFloat(pkt, 960)
			}
		}

		// Now decode packet 826
		pkt826 := packets[826]
		toc := gopus.ParseTOC(pkt826[0])
		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		var rd rangecoding.Decoder
		rd.Init(pkt826[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("Decode error: %v", err)
		}

		// Use 8k decoder for packet 826 (NB)
		libPcm, libSamples := libDec8k.DecodeFloat(pkt826, len(goNative)*2)
		libDec8k.Destroy()
		libDec12k.Destroy()

		delay := 5
		alignedLen := len(goNative)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < alignedLen; j++ {
			diff := goNative[j] - libPcm[j+delay]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j+delay] * libPcm[j+delay])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("   Stateful native SNR: %.1f dB", snr)

		// Show first samples
		t.Log("   First 10 samples:")
		for j := 0; j < 10 && j < alignedLen; j++ {
			t.Logf("      [%2d] go=%+9.6f lib=%+9.6f diff=%+9.6f",
				j, goNative[j], libPcm[j+delay], goNative[j]-libPcm[j+delay])
		}
	}
}
