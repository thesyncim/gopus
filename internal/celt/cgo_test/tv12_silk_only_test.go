// Package cgo tests SILK decoder only (no Opus wrapper).
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12SilkDecoderOnly tests SILK decoder directly at 48kHz output.
// Uses DecodeWithDecoder which includes resampling.
func TestTV12SilkDecoderOnly(t *testing.T) {
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

	t.Log("=== SILK decoder only (with resampling to 48kHz) ===")

	prevBW := silk.Bandwidth(255)

	for i := 0; i <= 828; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		// Use DecodeWithDecoder (which includes resampling)
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goSamples, err := silkDec.DecodeWithDecoder(&rd, silkBW, toc.FrameSize, true)
		if err != nil {
			t.Logf("Packet %d: gopus error: %v", i, err)
			continue
		}

		// Decode with libopus at 48kHz
		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libSamples < 0 {
			continue
		}

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := goSamples[j] - libPcm[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j] * libPcm[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Log bandwidth changes and failing packets
		isBWChange := silkBW != prevBW
		prevBW = silkBW

		if isBWChange || snr < 40 || (i >= 824 && i <= 828) {
			marker := ""
			if isBWChange {
				marker = " <-- BW CHANGE"
			}
			t.Logf("Packet %4d: BW=%v, SNR=%6.1f dB%s", i, silkBW, snr, marker)

			// Show first samples for failing packets
			if snr < 40 && minLen > 0 {
				t.Log("  First 10 samples:")
				for j := 0; j < 10 && j < minLen; j++ {
					t.Logf("    [%2d] go=%+9.6f lib=%+9.6f diff=%+9.6f",
						j, goSamples[j], libPcm[j], goSamples[j]-libPcm[j])
				}
			}
		}
	}
}

// TestTV12SilkFreshVsStatefulAt48k tests fresh vs stateful SILK decoder at 48kHz output.
func TestTV12SilkFreshVsStatefulAt48k(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Log("=== SILK Fresh vs Stateful at 48kHz for packet 826 ===")

	// Test 1: Fresh decoder
	t.Log("\n1. Fresh SILK decoder for packet 826:")
	{
		silkDec := silk.NewDecoder()
		libDec, _ := NewLibopusDecoder(48000, 1)
		if libDec == nil {
			t.Skip("Could not create libopus decoder")
		}

		pkt826 := packets[826]
		toc := gopus.ParseTOC(pkt826[0])
		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

		var rd rangecoding.Decoder
		rd.Init(pkt826[1:])
		goSamples, err := silkDec.DecodeWithDecoder(&rd, silkBW, toc.FrameSize, true)
		if err != nil {
			t.Fatalf("Decode error: %v", err)
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt826, len(goSamples)*2)
		libDec.Destroy()

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := goSamples[j] - libPcm[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j] * libPcm[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("   Fresh SNR: %.1f dB", snr)
	}

	// Test 2: Stateful decoder (processed 0-825)
	t.Log("\n2. Stateful SILK decoder (processed 0-825):")
	{
		silkDec := silk.NewDecoder()
		libDec, _ := NewLibopusDecoder(48000, 1)
		if libDec == nil {
			t.Skip("Could not create libopus decoder")
		}

		// Build state
		for i := 0; i < 826; i++ {
			pkt := packets[i]
			toc := gopus.ParseTOC(pkt[0])
			if toc.Mode != gopus.ModeSILK {
				continue
			}
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if !ok {
				continue
			}

			var rd rangecoding.Decoder
			rd.Init(pkt[1:])
			silkDec.DecodeWithDecoder(&rd, silkBW, toc.FrameSize, true)
			libDec.DecodeFloat(pkt, 960*2)
		}

		// Decode packet 826
		pkt826 := packets[826]
		toc := gopus.ParseTOC(pkt826[0])
		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

		var rd rangecoding.Decoder
		rd.Init(pkt826[1:])
		goSamples, err := silkDec.DecodeWithDecoder(&rd, silkBW, toc.FrameSize, true)
		if err != nil {
			t.Fatalf("Decode error: %v", err)
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt826, len(goSamples)*2)
		libDec.Destroy()

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := goSamples[j] - libPcm[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j] * libPcm[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("   Stateful SNR: %.1f dB", snr)

		// Show first samples
		if snr < 40 {
			t.Log("   First 10 samples:")
			for j := 0; j < 10 && j < minLen; j++ {
				t.Logf("      [%2d] go=%+9.6f lib=%+9.6f diff=%+9.6f",
					j, goSamples[j], libPcm[j], goSamples[j]-libPcm[j])
			}
		}
	}
}
