// Package cgo analyzes packet 137 (first NB→MB transition).
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Pkt137Analysis analyzes the first bandwidth transition.
func TestTV12Pkt137Analysis(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 150)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create separate decoders to test different scenarios
	silkDec := silk.NewDecoder()

	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Processing packets 0-136 (NB) ===")

	// Process NB packets (0-136)
	for i := 0; i <= 136; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// libopus
		libDec.DecodeFloat(pkt, 1920)

		// gopus SILK
		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
			}
		}
	}

	// Check sMid after last NB packet (136)
	sMidAfter136 := silkDec.GetSMid()
	t.Logf("sMid AFTER pkt 136 (last NB): [%d, %d]", sMidAfter136[0], sMidAfter136[1])

	// Decode packet 137 (first MB)
	pkt := packets[137]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("\n=== Packet 137: Mode=%v BW=%d (MB) ===", toc.Mode, toc.Bandwidth)

	// libopus 48kHz
	libOut, libN := libDec.DecodeFloat(pkt, 1920)

	// gopus SILK - decode to 48kHz via Decode()
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	goOut, _ := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)

	// Also decode at native rate
	silkDec2 := silk.NewDecoder()
	for i := 0; i <= 136; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				duration := silk.FrameDurationFromTOC(toc.FrameSize)
				var rd rangecoding.Decoder
				rd.Init(pkt[1:])
				silkDec2.DecodeFrame(&rd, silkBW, duration, true)
			}
		}
	}

	sMidBefore137 := silkDec2.GetSMid()
	t.Logf("sMid BEFORE pkt 137 (first MB): [%d, %d]", sMidBefore137[0], sMidBefore137[1])
	t.Logf("sMid[1] as float: %.6f", float32(sMidBefore137[1])/32768.0)

	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goNative, _ := silkDec2.DecodeFrame(&rd, silkBW, duration, true)

	// Compare outputs
	minLen := len(goOut)
	if libN < minLen {
		minLen = libN
	}

	var sumSqErr, sumSqSig float64
	for j := 0; j < minLen; j++ {
		diff := goOut[j] - libOut[j]
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(libOut[j] * libOut[j])
	}
	snr := 10 * math.Log10(sumSqSig/sumSqErr)

	t.Logf("\n48kHz output: gopus=%d samples, libopus=%d samples, SNR=%.1f dB", len(goOut), libN, snr)

	// Show first 10 samples
	t.Log("\nFirst 10 samples at 48kHz:")
	for i := 0; i < 10 && i < minLen; i++ {
		diff := goOut[i] - libOut[i]
		t.Logf("  [%d] go=%.6f lib=%.6f diff=%.6f", i, goOut[i], libOut[i], diff)
	}

	// Show native samples
	t.Logf("\nNative (12kHz) gopus first 10 samples:")
	for i := 0; i < 10 && i < len(goNative); i++ {
		t.Logf("  [%d] %.6f", i, goNative[i])
	}
}
