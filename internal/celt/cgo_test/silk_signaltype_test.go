// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkSignalTypes checks the signal type (voiced/unvoiced) for each packet.
func TestSilkSignalTypes(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Log("Signal types for packets in testvector02:")

	for pktIdx := 0; pktIdx < len(packets) && pktIdx < 10; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			t.Logf("Packet %d: Not SILK mode", pktIdx)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Decode to extract signal type info
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		dec := silk.NewDecoder()
		_, err := dec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", pktIdx, err)
			continue
		}

		// Get signal type from decoder's internal state
		// We need to expose this from the silk package
		signalType := dec.GetLastSignalType()
		signalTypeName := "unknown"
		switch signalType {
		case 0:
			signalTypeName = "inactive"
		case 1:
			signalTypeName = "unvoiced"
		case 2:
			signalTypeName = "voiced"
		}

		t.Logf("Packet %d: duration=%dms, signalType=%d (%s)",
			pktIdx, duration, signalType, signalTypeName)
	}
}
