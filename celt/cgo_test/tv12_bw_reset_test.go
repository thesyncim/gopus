//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests bandwidth transition with resampler reset.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12BandwidthResetAnalysis tests packet 826 with fresh vs accumulated decoders.
// If fresh decoder also fails, the issue is in the packet itself.
// If fresh decoder passes but accumulated fails, the issue is state accumulation.
func TestTV12BandwidthResetAnalysis(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Test 1: Fresh decoder just for packet 826
	t.Log("=== Test 1: Fresh decoder just for packet 826 ===")
	{
		freshGo, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
		freshLib, _ := NewLibopusDecoder(48000, 1)
		if freshLib == nil {
			t.Skip("Could not create libopus decoder")
		}
		defer freshLib.Destroy()

		pkt := packets[826]
		toc := gopus.ParseTOC(pkt[0])
		t.Logf("Packet 826: BW=%d, Mode=%v", toc.Bandwidth, toc.Mode)

		goSamples, _ := decodeFloat32(freshGo, pkt)
		libPcm, libSamples := freshLib.DecodeFloat(pkt, len(goSamples)*2)

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		var sumSqErr, sumSqSig float64
		for i := 0; i < minLen; i++ {
			diff := goSamples[i] - libPcm[i]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[i] * libPcm[i])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("FRESH decoder SNR for packet 826: %.1f dB", snr)
	}

	// Test 2: Decoder that has processed only MB packets (no prior NB)
	t.Log("\n=== Test 2: Decoder with MB history only (packets 600-825) ===")
	{
		goMB, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
		libMB, _ := NewLibopusDecoder(48000, 1)
		if libMB == nil {
			t.Skip("Could not create libopus decoder")
		}
		defer libMB.Destroy()

		// Process only MB packets before 826
		for i := 600; i < 826; i++ {
			pkt := packets[i]
			toc := gopus.ParseTOC(pkt[0])
			if toc.Bandwidth != 1 { // 1 = MB
				continue
			}
			decodeFloat32(goMB, pkt)
			libMB.DecodeFloat(pkt, 960*2)
		}

		pkt := packets[826]
		goSamples, _ := decodeFloat32(goMB, pkt)
		libPcm, libSamples := libMB.DecodeFloat(pkt, len(goSamples)*2)

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		var sumSqErr, sumSqSig float64
		for i := 0; i < minLen; i++ {
			diff := goSamples[i] - libPcm[i]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[i] * libPcm[i])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("MB-only history decoder SNR for packet 826: %.1f dB", snr)
	}

	// Test 3: Full continuous decode from start
	t.Log("\n=== Test 3: Full continuous decode from packet 0 ===")
	{
		goCont, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
		libCont, _ := NewLibopusDecoder(48000, 1)
		if libCont == nil {
			t.Skip("Could not create libopus decoder")
		}
		defer libCont.Destroy()

		// Track bandwidth transitions
		t.Log("Bandwidth sequence around 826:")
		for i := 820; i <= 830 && i < len(packets); i++ {
			toc := gopus.ParseTOC(packets[i][0])
			t.Logf("  Packet %d: BW=%d", i, toc.Bandwidth)
		}

		// Full continuous decode
		for i := 0; i <= 826; i++ {
			pkt := packets[i]
			decodeFloat32(goCont, pkt)
			libCont.DecodeFloat(pkt, 960*2)
		}

		// Now decode 826 and compare (already decoded above, re-check state)
		// Need to decode 826 fresh comparison
	}

	// Test 4: Check if there's NB packets before 826 that would have used the 8kHz resampler
	t.Log("\n=== Test 4: Find all NB packets before 826 ===")
	{
		nbPackets := []int{}
		for i := 0; i < 826; i++ {
			toc := gopus.ParseTOC(packets[i][0])
			if toc.Bandwidth == 0 { // 0 = NB
				nbPackets = append(nbPackets, i)
			}
		}
		t.Logf("NB packets before 826: %d total", len(nbPackets))
		if len(nbPackets) > 0 {
			t.Logf("First NB: %d, Last NB: %d", nbPackets[0], nbPackets[len(nbPackets)-1])
			if len(nbPackets) > 5 {
				t.Logf("Last 5 NB packets: %v", nbPackets[len(nbPackets)-5:])
			} else {
				t.Logf("All NB packets: %v", nbPackets)
			}
		}
	}
}

// TestTV12ResamplerStateOnBWChange verifies resampler state on bandwidth change.
func TestTV12ResamplerStateOnBWChange(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create decoder and process up to 825
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process packets 0-825
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		decodeFloat32(goDec, pkt)
		libDec.DecodeFloat(pkt, 960*2)
	}

	// Now decode 826 (BW transition)
	pkt826 := packets[826]
	go826, _ := decodeFloat32(goDec, pkt826)
	lib826, libN := libDec.DecodeFloat(pkt826, len(go826)*2)

	// Compare
	minLen := len(go826)
	if libN < minLen {
		minLen = libN
	}

	var sumSqErr, sumSqSig float64
	var maxDiff float32
	maxDiffIdx := 0
	for i := 0; i < minLen; i++ {
		diff := go826[i] - lib826[i]
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(lib826[i] * lib826[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}
	snr := 10 * math.Log10(sumSqSig/sumSqErr)
	if math.IsNaN(snr) || math.IsInf(snr, 1) {
		snr = 999.0
	}

	t.Logf("Packet 826: SNR=%.1f dB, MaxDiff=%.6f at sample %d", snr, maxDiff, maxDiffIdx)

	// Show samples around max diff
	t.Log("Samples around max diff:")
	start := maxDiffIdx - 10
	if start < 0 {
		start = 0
	}
	end := maxDiffIdx + 11
	if end > minLen {
		end = minLen
	}
	for i := start; i < end; i++ {
		marker := ""
		if i == maxDiffIdx {
			marker = " <-- MAX"
		}
		t.Logf("  [%4d] go=%+9.6f lib=%+9.6f diff=%+9.6f%s",
			i, go826[i], lib826[i], go826[i]-lib826[i], marker)
	}

	// Decode 827 (should match again)
	pkt827 := packets[827]
	go827, _ := decodeFloat32(goDec, pkt827)
	lib827, libN827 := libDec.DecodeFloat(pkt827, len(go827)*2)

	minLen827 := len(go827)
	if libN827 < minLen827 {
		minLen827 = libN827
	}

	var sumSqErr827, sumSqSig827 float64
	for i := 0; i < minLen827; i++ {
		diff := go827[i] - lib827[i]
		sumSqErr827 += float64(diff * diff)
		sumSqSig827 += float64(lib827[i] * lib827[i])
	}
	snr827 := 10 * math.Log10(sumSqSig827/sumSqErr827)
	if math.IsNaN(snr827) || math.IsInf(snr827, 1) {
		snr827 = 999.0
	}

	t.Logf("Packet 827: SNR=%.1f dB", snr827)
}
