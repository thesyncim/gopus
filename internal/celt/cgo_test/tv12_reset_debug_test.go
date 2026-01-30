// Package cgo debugs why handleBandwidthChange isn't resetting the NB resampler.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResetDebug traces bandwidth changes and resampler resets.
func TestTV12ResetDebug(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	silkDec := silk.NewDecoder()

	// Track bandwidth changes
	var lastBW silk.Bandwidth = silk.BandwidthNarrowband
	bwNames := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}

	t.Log("=== Processing packets, watching for bandwidth transitions ===")

	for i := 0; i <= 826; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Skip non-SILK packets (Hybrid uses WB internally)
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		// Check for bandwidth transition
		if silkBW != lastBW {
			t.Logf("\n=== BANDWIDTH TRANSITION at packet %d: %s → %s ===",
				i, bwNames[lastBW], bwNames[silkBW])

			// Check NB resampler state BEFORE decode
			nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
			if nbRes != nil {
				sIIR := nbRes.GetSIIR()
				allZero := true
				for _, v := range sIIR {
					if v != 0 {
						allZero = false
						break
					}
				}
				t.Logf("  NB resampler BEFORE decode: sIIR[0]=%d, allZero=%v", sIIR[0], allZero)
			}

			// Clear debug flag before decode
			silkDec.DebugClearResetFlag()
		}

		// Decode using full Decode flow
		_, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
		}

		// Check if bandwidth changed
		if silkBW != lastBW {
			// Check if reset was called
			resetCalled := silkDec.DebugResetCalled()
			t.Logf("  Reset called: %v", resetCalled)

			// Check NB resampler state AFTER decode
			nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
			if nbRes != nil {
				sIIR := nbRes.GetSIIR()
				allZero := true
				for _, v := range sIIR {
					if v != 0 {
						allZero = false
						break
					}
				}
				t.Logf("  NB resampler AFTER decode: sIIR[0]=%d, allZero=%v", sIIR[0], allZero)
			}

			// Get pre/post reset states
			pre, post := silkDec.DebugGetResetStates()
			if silkBW == silk.BandwidthNarrowband {
				t.Logf("  Debug states captured: pre[0]=%d, post[0]=%d", pre[0], post[0])
			}

			lastBW = silkBW
		}
	}
}
