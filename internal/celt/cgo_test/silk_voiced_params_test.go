// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkVoicedParams examines parameters of voiced packets to find divergence pattern.
func TestSilkVoicedParams(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil {
		t.Skip("Could not load packets")
	}

	voicedPackets := []int{4, 5, 11, 12, 13, 14, 15, 19}
	divergentPackets := map[int]bool{4: true, 5: true, 15: true}

	t.Log("Voiced packet parameters:")
	t.Log("(Divergent packets marked with *)")

	for _, pktIdx := range voicedPackets {
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

		// Decode to extract parameters
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goDec := silk.NewDecoder()
		_, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			continue
		}

		// Get internal state info
		params := goDec.GetLastFrameParams()

		marker := ""
		if divergentPackets[pktIdx] {
			marker = " *DIVERGENT*"
		}

		t.Logf("Packet %2d%s:", pktIdx, marker)
		t.Logf("  Bandwidth: %d kHz", config.SampleRate/1000)
		t.Logf("  NLSFInterpCoefQ2: %d (interpFlag=%v)", params.NLSFInterpCoefQ2, params.NLSFInterpCoefQ2 < 4)
		t.Logf("  LagPrev: %d", params.LagPrev)
		t.Logf("  LTPScaleIndex: %d", params.LTPScaleIndex)
		t.Logf("  Gain indices: %v", params.GainIndices)
	}
}
