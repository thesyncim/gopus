// Package cgo compares full Opus decoders including Hybrid processing.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12FullOpusComparison tests with full Opus decoders (including Hybrid).
func TestTV12FullOpusComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create FULL Opus decoders
	goDec, _ := gopus.NewDecoder(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Full Opus Decoder Comparison (including Hybrid) ===")

	// Get SILK decoder for state tracing
	silkDec := goDec.GetSILKDecoder()

	// Process ALL packets through BOTH decoders
	for i := 0; i <= 828 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Log state at key packets
		if i == 825 || i == 826 {
			nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
			if nbRes != nil {
				sIIR := nbRes.GetSIIR()
				t.Logf("Pkt %d BEFORE decode (%v %v): NB sIIR[0:3]=[%d,%d,%d], sMid=%v",
					i, toc.Mode, toc.Bandwidth, sIIR[0], sIIR[1], sIIR[2], silkDec.GetSMid())
			}
			// Clear debug flag before decode to track if reset is called
			silkDec.DebugClearResetFlag()
		}

		goOut, err := goDec.DecodeFloat32(pkt)
		if err != nil {
			t.Logf("Pkt %d: gopus error: %v", i, err)
			continue
		}
		libOut, libN := libDec.DecodeFloat(pkt, 1920)

		// Log state and compare at key packets
		if i == 825 || i == 826 || i == 827 {
			nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
			if nbRes != nil {
				sIIR := nbRes.GetSIIR()
				resetCalled := silkDec.DebugResetCalled()
				preReset, postReset := silkDec.DebugGetResetStates()
				t.Logf("Pkt %d AFTER decode: NB sIIR[0:3]=[%d,%d,%d], sMid=%v, resetCalled=%v",
					i, sIIR[0], sIIR[1], sIIR[2], silkDec.GetSMid(), resetCalled)
				if i == 826 {
					t.Logf("  Pre-reset sIIR[0:3]=[%d,%d,%d]", preReset[0], preReset[1], preReset[2])
					t.Logf("  Post-reset sIIR[0:3]=[%d,%d,%d]", postReset[0], postReset[1], postReset[2])
				}
			}

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

			t.Logf("Pkt %d: SNR=%.1f dB, go[0:3]=[%.6f,%.6f,%.6f] lib[0:3]=[%.6f,%.6f,%.6f]",
				i, snr, goOut[0], goOut[1], goOut[2], libOut[0], libOut[1], libOut[2])

			if i == 826 && snr < 30 {
				// Show more detail for packet 826
				t.Log("Ratio analysis for packet 826:")
				for j := 0; j < 20 && j < minLen; j++ {
					ratio := float32(0)
					if libOut[j] != 0 {
						ratio = goOut[j] / libOut[j]
					}
					t.Logf("  [%2d] go=%+.6f lib=%+.6f ratio=%.3f", j, goOut[j], libOut[j], ratio)
				}
			}
		}
	}
}
