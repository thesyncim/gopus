// Package cgo traces decoded parameters at packet 826
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ParamsTrace traces decoded parameters at packet 826.
func TestTV12ParamsTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Process packets 0-826
	for i := 0; i <= 826; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// For packet 826, use tracing version
		if i == 826 {
			t.Logf("\n=== Packet 826: BW=%s ===", silkBW)

			// Log the raw packet bytes
			t.Logf("Packet bytes (first 32): %v", pkt[:min(32, len(pkt))])

			// Decode with tracing
			var rd rangecoding.Decoder
			rd.Init(pkt[1:])

			goNative, err := silkDec.DecodeFrameWithTrace(&rd, silkBW, duration, true,
				func(frameIdx, k int, info silk.TraceInfo) {
					t.Logf("Frame %d, Subframe %d:", frameIdx, k)
					t.Logf("  SignalType: %d (0=inactive, 1=unvoiced, 2=voiced)", info.SignalType)
					t.Logf("  PitchLag: %d, LtpMemLength: %d, LpcOrder: %d", info.PitchLag, info.LtpMemLength, info.LpcOrder)
					t.Logf("  GainQ10: %d, InvGainQ31: %d", info.GainQ10, info.InvGainQ31)
					t.Logf("  LTPCoefQ14: %v", info.LTPCoefQ14)
					t.Logf("  A_Q12[0:5]: %v", info.A_Q12[:5])
					t.Logf("  FirstExcQ14: %d", info.FirstExcQ14)
					t.Logf("  FirstPresQ14: %d", info.FirstPresQ14)
					t.Logf("  FirstLpcPredQ10: %d", info.FirstLpcPredQ10)
					t.Logf("  FirstSLPC: %d", info.FirstSLPC)
					t.Logf("  FirstOutputQ0: %d", info.FirstOutputQ0)
					t.Logf("  SLPCHistory[0:5]: %v", info.SLPCHistory[:5])
					if info.SignalType == 2 { // voiced
						t.Logf("  SLTPQ15Used: %v", info.SLTPQ15Used)
						t.Logf("  FirstLTPPredQ13: %d", info.FirstLTPPredQ13)
					}
				})
			if err != nil {
				t.Errorf("Decode error: %v", err)
			}

			t.Logf("\nOutput samples: %d", len(goNative))
			t.Logf("First 20 output samples:")
			for j := 0; j < 20 && j < len(goNative); j++ {
				t.Logf("  [%3d] %+9.6f (int16: %d)", j, goNative[j], int16(goNative[j]*32768))
			}

			// Get decoder state
			state := silkDec.GetDecoderState()
			t.Logf("\nDecoder state:")
			t.Logf("  FsKHz: %d, LPCOrder: %d, NbSubfr: %d", state.FsKHz, state.LPCOrder, state.NbSubfr)
			t.Logf("  PrevNLSFQ15[0:5]: %v", state.PrevNLSFQ15[:min(5, len(state.PrevNLSFQ15))])

			params := silkDec.GetLastFrameParams()
			t.Logf("\nLast frame params:")
			t.Logf("  NLSFInterpCoefQ2: %d", params.NLSFInterpCoefQ2)
			t.Logf("  LTPScaleIndex: %d", params.LTPScaleIndex)
			t.Logf("  LagPrev: %d", params.LagPrev)
			t.Logf("  GainIndices: %v", params.GainIndices)

		} else {
			// Normal decode to maintain state
			var rd rangecoding.Decoder
			rd.Init(pkt[1:])
			_, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
			if err != nil {
				continue
			}
		}
	}
}
