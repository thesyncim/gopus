// Package cgo traces bandwidth changes and resampler state for TV12.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12BandwidthTrace traces bandwidth changes and resampler state.
func TestTV12BandwidthTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create SILK decoder directly to trace state
	silkDec := silk.NewDecoder()

	// Track bandwidth transitions
	prevBW := silk.Bandwidth(255)
	prevMode := ""

	t.Log("=== TV12 Bandwidth Transition Trace ===")

	// Process packets 0-826 with SILK decoder
	for i := 0; i <= 826 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Get mode and bandwidth strings
		modeName := "?"
		bwName := "?"
		var silkBW silk.Bandwidth
		switch toc.Mode {
		case gopus.ModeSILK:
			modeName = "SILK"
			silkBW, _ = silk.BandwidthFromOpus(int(toc.Bandwidth))
		case gopus.ModeHybrid:
			modeName = "Hybrid"
			silkBW = silk.BandwidthWideband // Hybrid uses WB for SILK
		default:
			continue // Skip CELT-only
		}
		switch silkBW {
		case silk.BandwidthNarrowband:
			bwName = "NB"
		case silk.BandwidthMediumband:
			bwName = "MB"
		case silk.BandwidthWideband:
			bwName = "WB"
		}

		// Check resampler state BEFORE decode at transitions
		if silkBW != prevBW || i == 136 || i == 385 || i == 607 || i == 757 || i == 825 || i == 826 {
			nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
			if nbRes != nil {
				sIIR := nbRes.GetSIIR()
				hasState := sIIR[0] != 0 || sIIR[1] != 0 || sIIR[2] != 0
				t.Logf("Packet %4d: BEFORE decode, NB resampler sIIR[0:3]=[%d,%d,%d] hasState=%v",
					i, sIIR[0], sIIR[1], sIIR[2], hasState)
			}
		}

		// Log transitions
		if bwName != "" && (modeName != prevMode || silkBW != prevBW) {
			t.Logf("Packet %4d: %s %s (transition)", i, modeName, bwName)
			prevMode = modeName
		}

		// Decode with SILK decoder (uses handleBandwidthChange)
		if toc.Mode == gopus.ModeSILK {
			silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		}
		// Note: We skip Hybrid packets for this test since we're tracing SILK-only path

		prevBW = silkBW
	}

	// Final state check
	t.Log("\n=== Final Resampler State ===")
	for _, bw := range []silk.Bandwidth{silk.BandwidthNarrowband, silk.BandwidthMediumband, silk.BandwidthWideband} {
		res := silkDec.GetResampler(bw)
		if res == nil {
			continue
		}
		sIIR := res.GetSIIR()
		bwStr := []string{"NB", "MB", "WB"}[bw]
		t.Logf("%s resampler: sIIR=%v", bwStr, sIIR)
	}
	t.Logf("sMid: %v", silkDec.GetSMid())
}

// TestTV12HybridBandwidthTracking verifies that Hybrid mode properly updates
// the SILK decoder's bandwidth tracking for correct resampler reset.
func TestTV12HybridBandwidthTracking(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create Opus-level decoder
	goDec, _ := gopus.NewDecoder(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== TV12 Hybrid Bandwidth Tracking Test ===")
	t.Log("Packet sequence: NB(0-136) → MB(137-213) → WB(214-385) → Hybrid(386-607) → WB(608-757) → MB(758-825) → NB(826+)")

	// Process all packets through both decoders, trace resampler state at key points
	silkDec := goDec.GetSILKDecoder()

	for i := 0; i <= 828 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Log resampler state BEFORE decode at key transitions
		if i == 136 || i == 137 || i == 385 || i == 386 || i == 607 || i == 608 || i == 757 || i == 758 || i == 825 || i == 826 {
			nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
			if nbRes != nil {
				sIIR := nbRes.GetSIIR()
				t.Logf("Pkt %4d BEFORE (%v %v): NB sIIR[0:3]=[%d,%d,%d]",
					i, toc.Mode, toc.Bandwidth, sIIR[0], sIIR[1], sIIR[2])
			}
		}

		goOut, _ := goDec.DecodeFloat32(pkt)
		libOut, libN := libDec.DecodeFloat(pkt, 1920)

		// Check SNR at transition points
		if i == 826 || i == 827 || i == 828 {
			minLen := len(goOut)
			if libN < minLen {
				minLen = libN
			}
			var sumSqErr, sumSqSig float64
			for j := 0; j < minLen; j++ {
				diff := goOut[j] - libOut[j]
				sumSqErr += float64(diff * diff)
				sumSqSig += float64(libOut[j] * libOut[j])
			}
			snr := 10 * math.Log10(sumSqSig/sumSqErr)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999.0
			}

			t.Logf("Packet %d (%v): SNR=%.1f dB, go[0]=%.6f lib[0]=%.6f",
				i, toc.Mode, snr, goOut[0], libOut[0])

			// Log resampler state AFTER decode
			nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
			if nbRes != nil {
				sIIR := nbRes.GetSIIR()
				t.Logf("  AFTER: NB sIIR[0:3]=[%d,%d,%d]", sIIR[0], sIIR[1], sIIR[2])
			}

			if i == 826 && snr < 30 {
				t.Logf("  WARNING: Low SNR at packet 826 suggests resampler state issue")
				t.Logf("  First 5 samples: go=%v", goOut[:5])
				t.Logf("                  lib=%v", libOut[:5])
			}
		}
	}
}
