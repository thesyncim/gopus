// Package cgo traces sMid state across bandwidth change in TV12.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12SMidTrace traces sMid state at bandwidth transition.
func TestTV12SMidTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	t.Log("=== sMid State at BW Transition ===")

	for i := 0; i < 145 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		// Get sMid BEFORE decode
		smidBefore := silkDec.GetSMid()

		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		// Decode at native rate
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			continue
		}

		// Get sMid AFTER decode (but before building resampler input)
		smidAfterDecode := silkDec.GetSMid()

		// Only log around transition
		if i < 134 || i > 140 {
			continue
		}

		marker := ""
		if i == 137 {
			marker = " <-- BW CHANGE (NB->MB)"
		}

		t.Logf("\nPacket %d: BW=%v (%dkHz)%s", i, silkBW, config.SampleRate/1000, marker)
		t.Logf("  sMid BEFORE decode: [%d, %d]", smidBefore[0], smidBefore[1])
		t.Logf("  sMid AFTER decode:  [%d, %d]", smidAfterDecode[0], smidAfterDecode[1])

		// Show first and last native samples
		if len(nativeSamples) >= 4 {
			t.Logf("  Native samples (first 4): [%.6f, %.6f, %.6f, %.6f]",
				nativeSamples[0], nativeSamples[1], nativeSamples[2], nativeSamples[3])
			lastIdx := len(nativeSamples)
			t.Logf("  Native samples (last 4):  [%.6f, %.6f, %.6f, %.6f]",
				nativeSamples[lastIdx-4], nativeSamples[lastIdx-3],
				nativeSamples[lastIdx-2], nativeSamples[lastIdx-1])
		}

		// For packet 137, show what the resampler input would look like
		if i == 137 {
			t.Log("\n  === Resampler Input Analysis ===")
			t.Logf("  sMid[1] from prev frame (8kHz): %d (%.6f as float32)",
				smidBefore[1], float32(smidBefore[1])/32768.0)
			t.Logf("  First native sample (12kHz):   %.6f", nativeSamples[0])
			t.Logf("  resamplerInput[0] would be:    %.6f (from sMid[1])",
				float32(smidBefore[1])/32768.0)
			t.Logf("  resamplerInput[1] would be:    %.6f (from native[0])",
				nativeSamples[0])

			// Check if sMid[1] is appropriate for 12kHz resampler
			t.Log("\n  >>> ISSUE: sMid[1] is from 8kHz native rate")
			t.Log("  >>> But packet 137 resampler expects 12kHz input")
			t.Log("  >>> This mismatch causes the first resampler input to be wrong")
		}
	}
}
