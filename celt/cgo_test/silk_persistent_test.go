//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestSilkPersistentStateNative compares native rate output with persistent decoders.
// This test uses both Go and libopus decoders that persist across packets,
// with proper delay alignment, to isolate state persistence issues.
func TestSilkPersistentStateNative(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil {
		t.Skip("Could not load packets")
	}

	config := silk.GetBandwidthConfig(silk.BandwidthNarrowband)
	delay := 5 // 8kHz delay

	// Create persistent decoders
	goDec := silk.NewDecoder()
	libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDec == nil {
		t.Fatal("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	signalTypes := []string{"inactive", "unvoiced", "voiced"}

	t.Logf("Testing with persistent decoders at native rate (%dHz):", config.SampleRate)

	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			t.Logf("Packet %2d: SKIP (not SILK)", pktIdx)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Decode with persistent Go decoder
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("Go decode packet %d failed: %v", pktIdx, err)
		}

		sigType := goDec.GetLastSignalType()
		sigName := signalTypes[sigType]

		// Decode with persistent libopus decoder
		// Note: maxSamples should match native frame size
		framesPerPacket := int(duration) / 20
		if framesPerPacket < 1 {
			framesPerPacket = 1
		}
		nativeFrameSize := framesPerPacket * 160 // 8kHz, 20ms = 160 samples
		libPcm, libSamples := libDec.DecodeFloat(pkt, nativeFrameSize)
		if libSamples < 0 {
			t.Fatalf("libopus decode packet %d failed", pktIdx)
		}

		// Calculate aligned SNR
		alignedLen := len(goNative)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}
		if alignedLen < 0 {
			alignedLen = 0
		}

		var sumSqErr, sumSqSig float64
		exactMatches := 0
		for i := 0; i < alignedLen; i++ {
			goVal := goNative[i]
			libVal := libPcm[i+delay]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff == 0 {
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
		if alignedLen > 0 {
			exactPct = 100.0 * float64(exactMatches) / float64(alignedLen)
		}

		t.Logf("Packet %2d %-9s: SNR=%6.1f dB, exact=%5.1f%% (%d/%d) %s",
			pktIdx, sigName, snr, exactPct, exactMatches, alignedLen, status)

		// If SNR is low, show first few differences
		if snr < 40.0 && alignedLen > 0 {
			diffCount := 0
			for i := 0; i < alignedLen && diffCount < 5; i++ {
				goVal := goNative[i]
				libVal := libPcm[i+delay]
				if goVal != libVal {
					t.Logf("  Sample %d: go=%.6f, lib=%.6f, diff=%.6f", i, goVal, libVal, goVal-libVal)
					diffCount++
				}
			}
		}
	}
}

// TestSilkPersistentVsFresh compares persistent vs fresh decoders to isolate state issues.
func TestSilkPersistentVsFresh(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil {
		t.Skip("Could not load packets")
	}

	config := silk.GetBandwidthConfig(silk.BandwidthNarrowband)

	// Create persistent decoder
	persistentDec := silk.NewDecoder()

	t.Log("Comparing persistent vs fresh Go decoders:")

	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
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

		// Decode with persistent decoder
		var rdPersistent rangecoding.Decoder
		rdPersistent.Init(pkt[1:])
		persistentOut, err := persistentDec.DecodeFrame(&rdPersistent, silkBW, duration, true)
		if err != nil {
			t.Fatalf("Persistent decode failed: %v", err)
		}

		// Decode with fresh decoder
		freshDec := silk.NewDecoder()
		var rdFresh rangecoding.Decoder
		rdFresh.Init(pkt[1:])
		freshOut, err := freshDec.DecodeFrame(&rdFresh, silkBW, duration, true)
		if err != nil {
			t.Fatalf("Fresh decode failed: %v", err)
		}

		// Compare
		if len(persistentOut) != len(freshOut) {
			t.Errorf("Packet %d: length mismatch: persistent=%d, fresh=%d", pktIdx, len(persistentOut), len(freshOut))
			continue
		}

		var sumSqErr, sumSqSig float64
		matches := 0
		for i := 0; i < len(freshOut); i++ {
			diff := persistentOut[i] - freshOut[i]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(freshOut[i] * freshOut[i])
			if diff == 0 {
				matches++
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		status := "SAME"
		if snr < 999.0 {
			status = "DIFFER"
		}

		t.Logf("Packet %2d: SNR=%6.1f dB, exact=%5.1f%% %s", pktIdx, snr, 100.0*float64(matches)/float64(len(freshOut)), status)

		// Export and compare state if diverged
		if snr < 999.0 {
			persistentState := persistentDec.ExportState()
			freshState := freshDec.ExportState()
			t.Logf("  Persistent state: prevGainQ16=%d, fsKHz=%d", persistentState.PrevGainQ16, persistentState.FsKHz)
			t.Logf("  Fresh state:      prevGainQ16=%d, fsKHz=%d", freshState.PrevGainQ16, freshState.FsKHz)
			if persistentState.PrevGainQ16 != freshState.PrevGainQ16 {
				t.Logf("  WARNING: prevGainQ16 differs!")
			}
		}
	}

	// Also decode to get state at native rate (before resampling) to see if that's consistent
	t.Log("\nLibopus comparison with persistent Go decoder:")
	libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Reset persistent decoder
	persistentDec.Reset()

	delay := 5
	for pktIdx := 0; pktIdx < 5; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Decode with Go
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goOut, _ := persistentDec.DecodeFrame(&rd, silkBW, duration, true)

		// Decode with libopus
		framesPerPacket := int(duration) / 20
		if framesPerPacket < 1 {
			framesPerPacket = 1
		}
		nativeFrameSize := framesPerPacket * 160
		libPcm, libSamples := libDec.DecodeFloat(pkt, nativeFrameSize)

		alignedLen := len(goOut)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}

		var sumSqErr, sumSqSig float64
		for i := 0; i < alignedLen; i++ {
			diff := goOut[i] - libPcm[i+delay]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[i+delay] * libPcm[i+delay])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("Packet %d: go vs lib SNR=%6.1f dB", pktIdx, snr)
	}
}
