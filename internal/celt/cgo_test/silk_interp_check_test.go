// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestNLSFInterpCheck checks NLSFInterpCoefQ2 values for divergent packets.
func TestNLSFInterpCheck(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Check packets 4, 5, 15 (divergent) and 11 (bit-exact) for comparison
	packetsToCheck := []int{4, 5, 11, 15}

	for _, pktIdx := range packetsToCheck {
		if pktIdx >= len(packets) {
			continue
		}
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
		config := silk.GetBandwidthConfig(silkBW)

		// Decode with gopus and extract parameters
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goDec := silk.NewDecoder()

		t.Logf("\n=== Packet %d ===", pktIdx)

		// We need to decode frame by frame to see the parameters
		// This requires accessing internal state which we can't do directly
		// Instead, let's decode the full packet and check the final signal type
		_, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("  Error: %v", err)
			continue
		}

		signalType := goDec.GetLastSignalType()
		params := goDec.GetLastFrameParams()

		signalNames := []string{"inactive", "unvoiced", "voiced"}
		t.Logf("  Duration: %dms, SampleRate: %d, SignalType: %s",
			duration, config.SampleRate, signalNames[signalType])
		t.Logf("  NLSFInterpCoefQ2: %d (interpFlag=%v)",
			params.NLSFInterpCoefQ2, params.NLSFInterpCoefQ2 < 4)
		t.Logf("  LTPScaleIndex: %d, LagPrev: %d",
			params.LTPScaleIndex, params.LagPrev)
		t.Logf("  GainIndices: %v", params.GainIndices)
	}
}
