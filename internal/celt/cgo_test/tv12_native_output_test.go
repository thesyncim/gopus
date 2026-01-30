// Package cgo compares SILK native output before resampling
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12NativeOutput compares native SILK output for packets 136-137
// to isolate whether the issue is in SILK decoding or resampling.
func TestTV12NativeOutput(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 140)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus SILK decoder
	silkDec := silk.NewDecoder()

	// Process packets 0-137 to build state
	t.Log("Processing packets to build state...")

	for i := 0; i <= 137; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			t.Logf("Packet %d: non-SILK mode, skipping", i)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		var rd rangecoding.Decoder
		rd.Init(pkt[1:])

		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
			continue
		}

		// Log packets 135-137 in detail
		if i >= 135 && i <= 137 {
			bwName := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}[silkBW]
			t.Logf("\nPacket %d: %s, %d native samples", i, bwName, len(goNative))

			// Check for sign changes in native output
			var posCount, negCount int
			for j := 0; j < len(goNative); j++ {
				if goNative[j] > 0 {
					posCount++
				} else if goNative[j] < 0 {
					negCount++
				}
			}
			t.Logf("  Positive samples: %d, Negative samples: %d", posCount, negCount)

			// Dump first 20 native samples
			t.Log("  First 20 native samples:")
			for j := 0; j < 20 && j < len(goNative); j++ {
				t.Logf("    [%3d] %+.6f (int16: %d)", j, goNative[j], int16(goNative[j]*32768))
			}

			// For packet 137 (MB), sample 121 at 48kHz = ~30 at native 12kHz
			// Let's check around native sample 30
			if i == 137 {
				t.Log("  Samples around native sample 30 (corresponds to 48kHz sample ~121):")
				for j := 25; j < 35 && j < len(goNative); j++ {
					t.Logf("    [%3d] %+.6f", j, goNative[j])
				}
			}

			// Check the sMid state after this packet
			state := silkDec.ExportState()
			t.Logf("  After decode: fsKHz=%d, prevGainQ16=%d", state.FsKHz, state.PrevGainQ16)
			t.Logf("  Native sample rate: %d Hz", config.SampleRate)
		}
	}
}
