// Package cgo compares native SILK output at packet 137.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12NativeAt137 compares native SILK output at packet 137.
func TestTV12NativeAt137(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus SILK decoder for native rate
	silkDec := silk.NewDecoder()

	// Create libopus decoders at native rates
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	libDec12k, _ := NewLibopusDecoder(12000, 1)
	if libDec8k == nil || libDec12k == nil {
		t.Skip("Could not create native rate decoders")
	}
	defer libDec8k.Destroy()
	defer libDec12k.Destroy()

	t.Log("=== Native SILK Output at Packet 137 ===")

	// Decode packets 0-136 to warm up state
	for i := 0; i < 137; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Update libopus state
		if toc.Bandwidth == 0 {
			libDec8k.DecodeFloat(pkt, 320)
		} else {
			libDec12k.DecodeFloat(pkt, 480)
		}

		// Update gopus SILK state
		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				duration := silk.FrameDurationFromTOC(toc.FrameSize)
				var rd rangecoding.Decoder
				rd.Init(pkt[1:])
				silkDec.DecodeFrame(&rd, silkBW, duration, true)
			}
		}
	}

	// Now decode packet 137
	pkt137 := packets[137]
	toc137 := gopus.ParseTOC(pkt137[0])

	t.Logf("Packet 137: Mode=%v BW=%d", toc137.Mode, toc137.Bandwidth)

	// Decode with gopus SILK at native rate (12kHz for MB)
	silkBW, _ := silk.BandwidthFromOpus(int(toc137.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc137.FrameSize)
	var rd rangecoding.Decoder
	rd.Init(pkt137[1:])
	goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}

	// Decode with libopus at 12kHz
	libNative, libN := libDec12k.DecodeFloat(pkt137, 480)
	if libN <= 0 {
		t.Fatal("libopus decode error")
	}

	t.Logf("gopus native samples: %d", len(goNative))
	t.Logf("libopus native samples: %d", libN)

	// Compare native samples
	minLen := len(goNative)
	if libN < minLen {
		minLen = libN
	}

	var sumSqErr, sumSqSig float64
	for j := 0; j < minLen; j++ {
		diff := goNative[j] - libNative[j]
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(libNative[j] * libNative[j])
	}
	snr := 10 * math.Log10(sumSqSig/sumSqErr)
	t.Logf("Native rate SNR: %.1f dB", snr)

	t.Log("\nFirst 20 native samples:")
	for j := 0; j < 20 && j < minLen; j++ {
		ratio := float32(0)
		if libNative[j] != 0 {
			ratio = goNative[j] / libNative[j]
		}
		t.Logf("  [%2d] go=%+10.6f lib=%+10.6f diff=%+10.6f ratio=%.3f",
			j, goNative[j], libNative[j], goNative[j]-libNative[j], ratio)
	}
}
