// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkMultiPacketSNR tests SNR when decoding multiple packets with persistent state.
func TestSilkMultiPacketSNR(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	// Create persistent decoders
	goDec := silk.NewDecoder()
	config := silk.GetBandwidthConfig(silk.BandwidthNarrowband)
	libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDec == nil {
		t.Fatal("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("Decoding packets 0-4 with persistent state:")
	for pktIdx := 0; pktIdx < 5 && pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			t.Logf("  Packet %d: SKIP (not SILK)", pktIdx)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Decode with Go
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goOut, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("Go decode packet %d failed: %v", pktIdx, err)
		}

		// Decode with libopus
		libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
		if libSamples < 0 {
			t.Fatalf("libopus decode packet %d failed", pktIdx)
		}

		// Calculate SNR
		minN := len(goOut)
		if libSamples < minN {
			minN = libSamples
		}

		var sigPow, noisePow float64
		for i := 0; i < minN; i++ {
			sig := float64(libPcm[i])
			noise := float64(goOut[i]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}
		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		status := "OK"
		if snr < 30 {
			status = "LOW SNR"
		}

		t.Logf("  Packet %d: SNR=%.1f dB %s", pktIdx, snr, status)
	}
}

// TestSilkOutBufCompare compares outBuf state between Go and libopus after each frame.
func TestSilkOutBufCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	// Test packet 4
	pkt := packets[4]
	toc := gopus.ParseTOC(pkt[0])
	if toc.Mode != gopus.ModeSILK {
		t.Skip("Not SILK mode")
	}

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	config := silk.GetBandwidthConfig(silkBW)
	fsKHz := config.SampleRate / 1000
	nbSubfr := 4
	if duration == 10 {
		nbSubfr = 2
	}
	framesPerPacket := int(duration) / 20
	if framesPerPacket < 1 {
		framesPerPacket = 1
	}

	t.Logf("Comparing outBuf state for packet 4 (frames=%d)", framesPerPacket)

	for frameIdx := 0; frameIdx < framesPerPacket; frameIdx++ {
		libOutBuf, _, libPrevGain, _ := GetSilkOutBufState(pkt[1:], fsKHz, nbSubfr, framesPerPacket, frameIdx)
		if libOutBuf == nil {
			continue
		}

		// Check for non-zero values in outBuf
		nonZero := 0
		for i := 0; i < len(libOutBuf); i++ {
			if libOutBuf[i] != 0 {
				nonZero++
			}
		}

		t.Logf("  Frame %d: prevGainQ16=%d, outBuf nonZero=%d",
			frameIdx, libPrevGain, nonZero)

		// Show some outBuf values
		ltpMemLen := fsKHz * 20 // ltp_mem_length for 8/12/16 kHz
		if ltpMemLen+5 <= len(libOutBuf) {
			t.Logf("    outBuf[%d:%d] (near ltp_mem_length=%d): %v",
				ltpMemLen-5, ltpMemLen+5, ltpMemLen, libOutBuf[ltpMemLen-5:ltpMemLen+5])
		}
	}
}

// TestSilkStateBeforeFrame2 examines state BEFORE frame 2 to understand divergence.
func TestSilkStateBeforeFrame2(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	// Test packet 4
	pkt := packets[4]
	toc := gopus.ParseTOC(pkt[0])
	if toc.Mode != gopus.ModeSILK {
		t.Skip("Not SILK mode")
	}

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	config := silk.GetBandwidthConfig(silkBW)
	fsKHz := config.SampleRate / 1000
	nbSubfr := 4
	if duration == 10 {
		nbSubfr = 2
	}
	framesPerPacket := int(duration) / 20
	if framesPerPacket < 1 {
		framesPerPacket = 1
	}

	// Get libopus state BEFORE frame 2 (after decoding frames 0,1)
	frameBeforeDivergence := 1
	if framesPerPacket <= 2 {
		frameBeforeDivergence = framesPerPacket - 1
	}

	libOutBuf, libSLPCBuf, libPrevGain, err := GetSilkOutBufState(pkt[1:], fsKHz, nbSubfr, framesPerPacket, frameBeforeDivergence)
	if err != nil || libOutBuf == nil {
		t.Fatalf("Failed to get libopus outBuf state")
	}

	t.Logf("Libopus state AFTER frame %d:", frameBeforeDivergence)
	t.Logf("  prevGainQ16: %d", libPrevGain)
	t.Logf("  sLPCQ14Buf[0:4]: %v", libSLPCBuf[:4])

	// Count non-zero in different regions of outBuf
	ltpMemLen := fsKHz * 20 // 8*20=160 for 8kHz
	nonZeroBeforeLtp := 0
	nonZeroAfterLtp := 0
	for i := 0; i < ltpMemLen && i < len(libOutBuf); i++ {
		if libOutBuf[i] != 0 {
			nonZeroBeforeLtp++
		}
	}
	for i := ltpMemLen; i < len(libOutBuf); i++ {
		if libOutBuf[i] != 0 {
			nonZeroAfterLtp++
		}
	}
	t.Logf("  outBuf nonZero before ltp_mem_length=%d: %d", ltpMemLen, nonZeroBeforeLtp)
	t.Logf("  outBuf nonZero after ltp_mem_length: %d", nonZeroAfterLtp)

	// Show outBuf samples around ltpMemLength
	start := ltpMemLen - 10
	if start < 0 {
		start = 0
	}
	end := ltpMemLen + 10
	if end > len(libOutBuf) {
		end = len(libOutBuf)
	}
	t.Logf("  outBuf[%d:%d]: %v", start, end, libOutBuf[start:end])

	// Now decode with Go and compare
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()
	_, err = goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("Go decode failed: %v", err)
	}

	// Get Go decoder state (after ALL frames in the packet)
	goState := goDec.ExportState()
	t.Logf("\nGo decoder state (NOTE: after ALL %d frames, not just frame %d):", framesPerPacket, frameBeforeDivergence)
	t.Logf("  prevGainQ16: %d", goState.PrevGainQ16)
	t.Logf("  sLPCQ14Buf[0:4]: %v", goState.SLPCQ14Buf[:4])

	// The comparison isn't valid because libopus state is after frame 1, but Go state is after frame 2
	// Let's compare final state instead
	libFinalOutBuf, libFinalSLPCBuf, libFinalPrevGain, _ := GetSilkOutBufState(pkt[1:], fsKHz, nbSubfr, framesPerPacket, framesPerPacket-1)
	if libFinalOutBuf != nil {
		t.Logf("\nLibopus FINAL state (after all frames):")
		t.Logf("  prevGainQ16: %d", libFinalPrevGain)
		t.Logf("  sLPCQ14Buf[0:4]: %v", libFinalSLPCBuf[:4])

		// Compare final states
		t.Log("\nFinal state comparison:")
		if libFinalPrevGain != goState.PrevGainQ16 {
			t.Logf("  WARNING: prevGainQ16 mismatch: lib=%d, go=%d", libFinalPrevGain, goState.PrevGainQ16)
		} else {
			t.Logf("  MATCH: prevGainQ16=%d", libFinalPrevGain)
		}
		matches := 0
		for i := 0; i < 16; i++ {
			if libFinalSLPCBuf[i] == goState.SLPCQ14Buf[i] {
				matches++
			} else if i < 4 {
				t.Logf("  WARNING: sLPCQ14Buf[%d] mismatch: lib=%d, go=%d", i, libFinalSLPCBuf[i], goState.SLPCQ14Buf[i])
			}
		}
		t.Logf("  sLPCQ14Buf matches: %d/16", matches)
	}
}
