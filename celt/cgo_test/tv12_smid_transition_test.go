//go:build cgo_libopus
// +build cgo_libopus

// Package cgo traces sMid values across bandwidth transitions.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/silk"
)

// TestTV12SMidTransition826 traces sMid values around the MB→NB transition at packet 826.
func TestTV12SMidTransition826(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Create libopus decoder at 48kHz
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process packets and trace around packet 826
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

		// Get sMid BEFORE decode
		sMidBefore := silkDec.GetSMid()

		// Decode with gopus
		goOut, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		if err != nil {
			continue
		}

		// Get sMid AFTER decode
		sMidAfter := silkDec.GetSMid()

		// Trace packets 823-828 (around the MB→NB transition)
		if i >= 823 && i <= 828 {
			bwName := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}[silkBW]

			minLen := len(goOut)
			if libSamples < minLen {
				minLen = libSamples
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

			t.Logf("\n=== Packet %d: %s ===", i, bwName)
			t.Logf("sMid BEFORE: [%d, %d] (float: [%.6f, %.6f])",
				sMidBefore[0], sMidBefore[1],
				float32(sMidBefore[0])/32768.0, float32(sMidBefore[1])/32768.0)
			t.Logf("sMid AFTER:  [%d, %d] (float: [%.6f, %.6f])",
				sMidAfter[0], sMidAfter[1],
				float32(sMidAfter[0])/32768.0, float32(sMidAfter[1])/32768.0)
			t.Logf("48kHz SNR: %.1f dB", snr)

			// Show first 10 samples of output
			t.Logf("First 10 samples (go vs lib):")
			for j := 0; j < 10 && j < minLen; j++ {
				t.Logf("  [%d] go=%+.6f lib=%+.6f diff=%+.6f",
					j, goOut[j], libOut[j], goOut[j]-libOut[j])
			}

			// If this is packet 826 (first NB after MB), show more detail
			if i == 826 {
				t.Logf("\n** TRANSITION PACKET **")
				t.Logf("Previous bandwidth was MB (12kHz)")
				t.Logf("sMid values are from the PREVIOUS frame at 12kHz sample rate")
				t.Logf("But NB resampler (8→48kHz) treats them as 8kHz samples!")
			}
		}
	}
}

// TestTV12SMidTransition137 traces sMid values around the NB→MB transition at packet 137.
func TestTV12SMidTransition137(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 140)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Create libopus decoder at 48kHz
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process packets and trace around packet 137
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

		// Get sMid BEFORE decode
		sMidBefore := silkDec.GetSMid()

		// Decode with gopus
		goOut, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		if err != nil {
			continue
		}

		// Get sMid AFTER decode
		sMidAfter := silkDec.GetSMid()

		// Trace packets 134-139 (around the NB→MB transition)
		if i >= 134 && i <= 139 {
			bwName := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}[silkBW]

			minLen := len(goOut)
			if libSamples < minLen {
				minLen = libSamples
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

			t.Logf("\n=== Packet %d: %s ===", i, bwName)
			t.Logf("sMid BEFORE: [%d, %d] (float: [%.6f, %.6f])",
				sMidBefore[0], sMidBefore[1],
				float32(sMidBefore[0])/32768.0, float32(sMidBefore[1])/32768.0)
			t.Logf("sMid AFTER:  [%d, %d] (float: [%.6f, %.6f])",
				sMidAfter[0], sMidAfter[1],
				float32(sMidAfter[0])/32768.0, float32(sMidAfter[1])/32768.0)
			t.Logf("48kHz SNR: %.1f dB", snr)

			// Show first 10 samples of output
			t.Logf("First 10 samples (go vs lib):")
			for j := 0; j < 10 && j < minLen; j++ {
				t.Logf("  [%d] go=%+.6f lib=%+.6f diff=%+.6f",
					j, goOut[j], libOut[j], goOut[j]-libOut[j])
			}

			// If this is packet 137 (first MB after NB), show more detail
			if i == 137 {
				t.Logf("\n** TRANSITION PACKET **")
				t.Logf("Previous bandwidth was NB (8kHz)")
				t.Logf("sMid values are from the PREVIOUS frame at 8kHz sample rate")
				t.Logf("But MB resampler (12→48kHz) treats them as 12kHz samples!")
			}
		}
	}
}
