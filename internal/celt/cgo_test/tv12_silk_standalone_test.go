// Package cgo tests standalone SILK decoder vs libopus
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12SilkStandalone compares standalone SILK decoder with libopus
// using the exact same flow as TestTV12ResamplerIntermediateValues
func TestTV12SilkStandalone(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 140)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create libopus decoder at 48kHz mono
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Create standalone gopus SILK decoder
	goDec := silk.NewDecoder()

	t.Log("Comparing standalone SILK decoder with libopus...")

	// Process ALL packets together (like the working test)
	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Decode with libopus
		libOut, libSamples := libDec.DecodeFloat(pkt, 1920)

		// Skip non-SILK packets
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		// Decode with gopus standalone SILK decoder
		goOut, err := goDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		if err != nil {
			t.Logf("Packet %d: gopus decode error: %v", i, err)
			continue
		}

		// Compare
		minLen := len(goOut)
		if libSamples < minLen {
			minLen = libSamples
		}

		var sumSqErr, sumSqSig float64
		var maxDiff float32
		maxDiffIdx := 0

		for j := 0; j < minLen; j++ {
			diff := goOut[j] - libOut[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libOut[j] * libOut[j])
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
				maxDiffIdx = j
			}
		}

		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Log packets 135-140
		if i >= 135 && i < 140 {
			bwName := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}[silkBW]
			t.Logf("\nPacket %d: %s", i, bwName)
			t.Logf("  SNR=%.1f dB, MaxDiff=%.6f @ %d", snr, maxDiff, maxDiffIdx)

			// Show first 5 samples
			t.Log("  First 5 samples:")
			for j := 0; j < 5 && j < minLen; j++ {
				t.Logf("    [%d] lib=%+.6f go=%+.6f diff=%+.6f",
					j, libOut[j], goOut[j], goOut[j]-libOut[j])
			}
		}
	}
}

// TestTV12OpusVsSilk compares full Opus decoder with standalone SILK decoder
func TestTV12OpusVsSilk(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 140)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create full gopus decoder
	opusDec, err := gopus.NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("Failed to create Opus decoder: %v", err)
	}

	// Create standalone SILK decoder
	silkDec := silk.NewDecoder()

	t.Log("Comparing full Opus decoder with standalone SILK decoder...")

	// Process ALL packets
	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Decode with full Opus decoder
		opusOut, err := opusDec.DecodeFloat32(pkt)
		if err != nil {
			t.Logf("Packet %d: opus decode error: %v", i, err)
			continue
		}

		// Skip non-SILK packets
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		// Decode with standalone SILK decoder
		silkOut, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		if err != nil {
			t.Logf("Packet %d: silk decode error: %v", i, err)
			continue
		}

		// Compare
		minLen := len(opusOut)
		if len(silkOut) < minLen {
			minLen = len(silkOut)
		}

		var maxDiff float32
		maxDiffIdx := 0

		for j := 0; j < minLen; j++ {
			diff := opusOut[j] - silkOut[j]
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
				maxDiffIdx = j
			}
		}

		// Log packets 135-140
		if i >= 135 && i < 140 {
			bwName := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}[silkBW]
			t.Logf("\nPacket %d: %s", i, bwName)
			t.Logf("  MaxDiff=%.6f @ %d", maxDiff, maxDiffIdx)

			// Show first 5 samples
			t.Log("  First 5 samples:")
			for j := 0; j < 5 && j < minLen; j++ {
				t.Logf("    [%d] opus=%+.6f silk=%+.6f diff=%+.6f",
					j, opusOut[j], silkOut[j], opusOut[j]-silkOut[j])
			}
		}
	}
}
