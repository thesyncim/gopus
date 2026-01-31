// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkLTPParamsPacket4 traces LTP parameters for packet 4 (first voiced packet).
func TestSilkLTPParamsPacket4(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	// Decode packets 0-3 to build up state, then analyze packet 4
	goDec := silk.NewDecoder()

	for pktIdx := 0; pktIdx <= 4; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			t.Fatalf("Invalid SILK bandwidth for packet %d", pktIdx)
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		var rd rangecoding.Decoder
		rd.Init(pkt[1:])

		if pktIdx == 4 {
			// Enable tracing for packet 4
			t.Logf("\n=== Packet 4 Detailed Analysis ===")
			t.Logf("Bandwidth: %d kHz, Duration: %d ms", config.SampleRate/1000, duration)

			// Decode with tracing
			goNative, err := goDec.DecodeFrameWithTrace(&rd, silkBW, duration, true, func(frame, k int, info silk.TraceInfo) {
				if info.SignalType == 2 { // Voiced
					ltpOrder := 5
					startIdx := info.LtpMemLength - info.PitchLag - info.LpcOrder - ltpOrder/2
					t.Logf("Frame %d, k=%d: voiced, lag=%d, startIdx=%d",
						frame, k, info.PitchLag, startIdx)
					t.Logf("  invGainQ31=%d, gainQ10=%d", info.InvGainQ31, info.GainQ10)
					t.Logf("  LTPCoef[0..4]=%v", info.LTPCoefQ14)
					t.Logf("  firstPresQ14=%d, firstOutput=%d", info.FirstPresQ14, info.FirstOutputQ0)
				} else {
					t.Logf("Frame %d, k=%d: unvoiced, firstOutput=%d", frame, k, info.FirstOutputQ0)
				}
			})
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			t.Logf("\nDecoded %d samples", len(goNative))

			// Show first few samples of each frame
			t.Log("\nFrame 2 samples (where divergence occurs):")
			for i := 320; i < 330 && i < len(goNative); i++ {
				t.Logf("  sample[%d] = %d (int16)", i, int16(goNative[i]*32768))
			}
		} else {
			// Normal decode for packets 0-3
			_, err := goDec.DecodeFrame(&rd, silkBW, duration, pktIdx == 0)
			if err != nil {
				t.Fatalf("Decode failed for packet %d: %v", pktIdx, err)
			}
		}
	}
}
