//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests TV12 with full state processing of all packets.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/silk"
)

// TestTV12FullStateEvolution processes ALL packets to ensure state alignment.
func TestTV12FullStateEvolution(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create libopus 48kHz decoder
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Create gopus SILK decoder
	silkDec := silk.NewDecoder()

	// Track bandwidth and sample rate changes
	var prevBW silk.Bandwidth
	prevBWSet := false
	bwChanges := 0

	t.Log("Processing ALL packets 0-825...")

	// Count packets by mode and bandwidth
	silkCount := 0
	hybridCount := 0
	var silkBWCounts [3]int // NB, MB, WB

	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Decode with libopus
		libDec.DecodeFloat(pkt, 1920)

		if toc.Mode == gopus.ModeHybrid {
			hybridCount++
			continue
		}

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		silkCount++
		switch silkBW {
		case silk.BandwidthNarrowband:
			silkBWCounts[0]++
		case silk.BandwidthMediumband:
			silkBWCounts[1]++
		case silk.BandwidthWideband:
			silkBWCounts[2]++
		}

		if prevBWSet && prevBW != silkBW {
			bwChanges++
		}
		prevBW = silkBW
		prevBWSet = true

		// Decode with gopus
		silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	}

	t.Logf("Total SILK packets: %d (NB=%d, MB=%d, WB=%d)",
		silkCount, silkBWCounts[0], silkBWCounts[1], silkBWCounts[2])
	t.Logf("Total Hybrid packets: %d", hybridCount)
	t.Logf("SILK bandwidth changes: %d", bwChanges)

	// Get state before packet 826
	sMid := silkDec.GetSMid()
	t.Logf("sMid before packet 826: [%d, %d]", sMid[0], sMid[1])

	// Check what bandwidth was last used
	t.Logf("Last SILK bandwidth: %v", prevBW)

	// Now decode packet 826
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	t.Logf("\nPacket 826: Mode=%v, BW=%v", toc.Mode, toc.Bandwidth)
	t.Logf("Bandwidth change: %v -> %v", prevBW, silkBW)

	// Decode with gopus
	goOut, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}

	// Decode with libopus
	libOut, libN := libDec.DecodeFloat(pkt, 1920)

	t.Logf("Gopus output: %d samples", len(goOut))
	t.Logf("Libopus output: %d samples", libN)

	// Compare first 30 samples
	t.Log("\nFirst 30 samples at 48kHz:")
	for i := 0; i < 30 && i < len(goOut) && i < libN; i++ {
		t.Logf("  [%2d] go=%+.6f lib=%+.6f diff=%+.6f",
			i, goOut[i], libOut[i], goOut[i]-libOut[i])
	}

	// Check if gopus output is near zero
	nearZeroCount := 0
	for i := 0; i < 100 && i < len(goOut); i++ {
		if goOut[i] < 0.0001 && goOut[i] > -0.0001 {
			nearZeroCount++
		}
	}
	t.Logf("\nFirst 100 samples: %d near-zero (< 0.0001)", nearZeroCount)
}
