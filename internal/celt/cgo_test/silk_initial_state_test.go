// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkInitialState compares the first frame output sample-by-sample
// to identify where divergence originates in a fresh decoder.
func TestSilkInitialState(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 1)
	if err != nil || len(packets) < 1 {
		t.Skip("Could not load packets")
	}

	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	if toc.Mode != gopus.ModeSILK {
		t.Skip("Not SILK mode")
	}

	t.Logf("Packet 0: TOC=0x%02X, Mode=%d, BW=%d, FrameSize=%d",
		pkt[0], toc.Mode, toc.Bandwidth, toc.FrameSize)

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	t.Logf("SILK BW=%d, Duration=%dms", silkBW, duration)

	// gopus native decode - fresh decoder
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()
	goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus native decode failed: %v", err)
	}

	// libopus decode at 8k - fresh decoder
	libDec, err := NewLibopusDecoder(8000, 1)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder at 8k")
	}
	defer libDec.Destroy()

	libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	t.Logf("Samples: gopus=%d, libopus=%d", len(goNative), libSamples)

	// Check first 20 samples for exact match
	t.Log("First 20 samples comparison:")
	minN := 20
	if len(goNative) < minN {
		minN = len(goNative)
	}
	if libSamples < minN {
		minN = libSamples
	}

	exactMatches := 0
	for i := 0; i < minN; i++ {
		goVal := goNative[i]
		libVal := libPcm[i]
		diff := goVal - libVal
		marker := ""
		if diff == 0 {
			exactMatches++
		} else {
			marker = " *"
		}
		t.Logf("  [%2d] go=%.6f lib=%.6f diff=%.6f%s", i, goVal, libVal, diff, marker)
	}
	t.Logf("Exact matches: %d/%d", exactMatches, minN)

	// Check subframe boundaries (typically 40 samples at 8kHz for 5ms subframes)
	t.Log("\nSubframe boundary samples (every 40 samples):")
	for i := 0; i <= len(goNative)/40; i++ {
		idx := i * 40
		if idx >= len(goNative) || idx >= libSamples {
			break
		}
		goVal := goNative[idx]
		libVal := libPcm[idx]
		diff := goVal - libVal
		t.Logf("  Subframe %d start [%d]: go=%.6f lib=%.6f diff=%.6f", i, idx, goVal, libVal, diff)
	}
}
