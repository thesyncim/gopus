// Package cgo compares native SILK output between gopus.Decoder and silk.Decoder
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12NativeCompare compares native SILK decode output (before resampling)
// between gopus.Decoder and standalone silk.Decoder
func TestTV12NativeCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	pkt137 := packets[137]
	toc := gopus.ParseTOC(pkt137[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	t.Log("=== Comparing native decode (before resampling) at packet 137 ===")
	t.Logf("TOC: Mode=%v, BW=%d, FrameSize=%d", toc.Mode, toc.Bandwidth, toc.FrameSize)
	t.Logf("silkBW=%v", silkBW)

	// Path 1: Build state with gopus.Decoder, then decode packet 137 native
	var nativeGopus []float32
	var sMidGopusBefore [2]int16
	{
		goDec, _ := gopus.NewDecoderDefault(48000, 1)
		silkDec := goDec.GetSILKDecoder()

		// Process packets 0-136 via gopus
		for i := 0; i < 137; i++ {
			decodeFloat32(goDec, packets[i])
		}

		// Get sMid before decode
		sMidGopusBefore = silkDec.GetSMid()

		// Decode packet 137 at native rate using DecodeFrame
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		var rd rangecoding.Decoder
		rd.Init(pkt137[1:])
		nativeGopus, err = silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("gopus DecodeFrame error: %v", err)
		}
	}

	// Path 2: Build state with standalone silk.Decoder, then decode packet 137 native
	var nativeSilk []float32
	var sMidSilkBefore [2]int16
	{
		silkDec := silk.NewDecoder()

		// Process packets 0-136 via standalone silk
		for i := 0; i <= 136; i++ {
			pkt := packets[i]
			toc := gopus.ParseTOC(pkt[0])
			if toc.Mode == gopus.ModeSILK {
				silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
				if ok {
					silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
				}
			}
		}

		// Get sMid before decode
		sMidSilkBefore = silkDec.GetSMid()

		// Decode packet 137 at native rate using DecodeFrame
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		var rd rangecoding.Decoder
		rd.Init(pkt137[1:])
		nativeSilk, err = silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Fatalf("silk DecodeFrame error: %v", err)
		}
	}

	t.Logf("\n=== sMid state before packet 137 ===")
	t.Logf("gopus path sMid: [%d, %d]", sMidGopusBefore[0], sMidGopusBefore[1])
	t.Logf("silk path sMid:  [%d, %d]", sMidSilkBefore[0], sMidSilkBefore[1])

	t.Logf("\n=== Native output comparison ===")
	t.Logf("gopus native samples: %d", len(nativeGopus))
	t.Logf("silk native samples:  %d", len(nativeSilk))

	// Compare first 20 native samples
	t.Log("\nFirst 20 native samples:")
	minLen := len(nativeGopus)
	if len(nativeSilk) < minLen {
		minLen = len(nativeSilk)
	}
	maxDiff := float32(0)
	maxDiffIdx := 0
	for i := 0; i < 20 && i < minLen; i++ {
		diff := nativeGopus[i] - nativeSilk[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
		t.Logf("  [%2d] gopus=%.6f silk=%.6f diff=%.6f", i, nativeGopus[i], nativeSilk[i], nativeGopus[i]-nativeSilk[i])
	}

	// Check overall difference
	totalDiff := float32(0)
	for i := 0; i < minLen; i++ {
		diff := nativeGopus[i] - nativeSilk[i]
		if diff < 0 {
			diff = -diff
		}
		totalDiff += diff
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}
	t.Logf("\nMax diff: %.6f at sample %d", maxDiff, maxDiffIdx)
	t.Logf("Total absolute diff: %.6f", totalDiff)

	if totalDiff > 0.0001 {
		t.Log("\nNative samples DIFFER between gopus and silk paths!")
	} else {
		t.Log("\nNative samples are IDENTICAL between gopus and silk paths")
	}
}
