//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests persistent decoder across bandwidth changes.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12PersistentDecoder tests the full stream with a single persistent libopus decoder at 48kHz.
// This matches how the testvector test actually runs.
func TestTV12PersistentDecoder(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SINGLE 48kHz decoder that persists across all packets
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Persistent 48kHz decoder across all packets ===")

	// Track bandwidth transitions
	prevBW := gopus.Bandwidth(255)
	var bwTransitions []int

	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Bandwidth != prevBW {
			bwTransitions = append(bwTransitions, i)
		}
		prevBW = toc.Bandwidth
	}
	t.Logf("Bandwidth transitions at packets: %v", bwTransitions)

	// Decode all packets
	for i := 0; i <= 828; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
			t.Logf("Packet %d: gopus error: %v", i, err)
			continue
		}

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

		// Report around bandwidth transitions and failing packets
		isBWTransition := false
		for _, trans := range bwTransitions {
			if i == trans {
				isBWTransition = true
				break
			}
		}

		if isBWTransition || snr < 40 || (i >= 824 && i <= 828) {
			marker := ""
			if isBWTransition {
				marker = " <-- BW TRANSITION"
			}
			t.Logf("Packet %4d: BW=%d, Mode=%v, SNR=%6.1f dB%s",
				i, toc.Bandwidth, toc.Mode, snr, marker)
		}
	}
}

// TestTV12FreshVsStateAtBWChange tests fresh decoder vs stateful decoder at exact BW transition.
func TestTV12FreshVsStateAtBWChange(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Log("=== Comparing fresh vs stateful decoder at packet 826 ===")

	// Test 1: Stateful decoder
	t.Log("\nStateful decoder (processed 0-825 first):")
	{
		goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
		libDec, _ := NewLibopusDecoder(48000, 1)
		if libDec == nil {
			t.Skip("Could not create libopus decoder")
		}

		// Build state
		for i := 0; i < 826; i++ {
			decodeFloat32(goDec, packets[i])
			libDec.DecodeFloat(packets[i], 960*2)
		}

		// Decode 826
		go826, _ := decodeFloat32(goDec, packets[826])
		lib826, libN := libDec.DecodeFloat(packets[826], len(go826)*2)
		libDec.Destroy()

		minLen := len(go826)
		if libN < minLen {
			minLen = libN
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := go826[j] - lib826[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(lib826[j] * lib826[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("  Stateful SNR: %.1f dB", snr)
	}

	// Test 2: Fresh decoder (just packet 826)
	t.Log("\nFresh decoder (only packet 826):")
	{
		goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
		libDec, _ := NewLibopusDecoder(48000, 1)
		if libDec == nil {
			t.Skip("Could not create libopus decoder")
		}

		go826, _ := decodeFloat32(goDec, packets[826])
		lib826, libN := libDec.DecodeFloat(packets[826], len(go826)*2)
		libDec.Destroy()

		minLen := len(go826)
		if libN < minLen {
			minLen = libN
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := go826[j] - lib826[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(lib826[j] * lib826[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("  Fresh SNR: %.1f dB", snr)
	}

	// Test 3: Fresh decoder with state from packets 0-136 (NB) only, skip 137-825 (MB)
	t.Log("\nDecoder with NB-only state (0-136, skipping MB 137-825):")
	{
		goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
		libDec, _ := NewLibopusDecoder(48000, 1)
		if libDec == nil {
			t.Skip("Could not create libopus decoder")
		}

		// Only process NB packets
		for i := 0; i < 826; i++ {
			toc := gopus.ParseTOC(packets[i][0])
			if toc.Bandwidth != 0 { // Skip non-NB
				continue
			}
			decodeFloat32(goDec, packets[i])
			libDec.DecodeFloat(packets[i], 960*2)
		}

		go826, _ := decodeFloat32(goDec, packets[826])
		lib826, libN := libDec.DecodeFloat(packets[826], len(go826)*2)
		libDec.Destroy()

		minLen := len(go826)
		if libN < minLen {
			minLen = libN
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := go826[j] - lib826[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(lib826[j] * lib826[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("  NB-only state SNR: %.1f dB", snr)
	}

	// Test 4: State from packets 0-136 + explicitly reset between 136 and 826
	t.Log("\nDecoder reset between NB and MB segments:")
	{
		goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
		libDec, _ := NewLibopusDecoder(48000, 1)
		if libDec == nil {
			t.Skip("Could not create libopus decoder")
		}

		// Process NB packets 0-136
		for i := 0; i <= 136; i++ {
			toc := gopus.ParseTOC(packets[i][0])
			if toc.Bandwidth != 0 {
				continue
			}
			decodeFloat32(goDec, packets[i])
			libDec.DecodeFloat(packets[i], 960*2)
		}

		// Process MB packets 137-825
		for i := 137; i < 826; i++ {
			toc := gopus.ParseTOC(packets[i][0])
			if toc.Bandwidth != 1 {
				continue
			}
			decodeFloat32(goDec, packets[i])
			libDec.DecodeFloat(packets[i], 960*2)
		}

		// Now decode 826
		go826, _ := decodeFloat32(goDec, packets[826])
		lib826, libN := libDec.DecodeFloat(packets[826], len(go826)*2)
		libDec.Destroy()

		minLen := len(go826)
		if libN < minLen {
			minLen = libN
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := go826[j] - lib826[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(lib826[j] * lib826[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("  Continuous NB+MB state SNR: %.1f dB", snr)
	}
}
