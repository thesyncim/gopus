//go:build cgo_libopus
// +build cgo_libopus

// Package cgo performs deep analysis of packet 137 (first bandwidth change) in TV12.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12Packet137Deep analyzes the NB→MB transition at packet 137.
func TestTV12Packet137Deep(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder at 48kHz
	goDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}

	// Create libopus decoder at 48kHz
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== TV12 Packet 137 Deep Analysis ===")
	t.Log("NB→MB transition occurs at packet 137")

	// Process packets before transition
	for i := 0; i < 135; i++ {
		decodeFloat32(goDec, packets[i])
		libDec.DecodeFloat(packets[i], 1920)
	}

	// Analyze packets 135-144 (around the transition)
	for i := 135; i < 145 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
			continue
		}

		libPcm, libN := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libN <= 0 {
			continue
		}

		minLen := len(goSamples)
		if libN < minLen {
			minLen = libN
		}

		// Calculate SNR
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

		marker := ""
		if i == 137 {
			marker = " <-- BW CHANGE (NB→MB)"
		}

		t.Logf("\n=== Packet %d: Mode=%v BW=%d SNR=%.1f dB%s ===",
			i, toc.Mode, toc.Bandwidth, snr, marker)

		// Show detailed samples
		t.Log("First 20 samples:")
		for j := 0; j < 20 && j < minLen; j++ {
			diff := goSamples[j] - libPcm[j]
			ratio := float32(0)
			if libPcm[j] != 0 {
				ratio = goSamples[j] / libPcm[j]
			}
			sign := "  "
			if ratio < -0.5 && ratio > -2 {
				sign = "! " // Mark sign inversions
			}
			t.Logf("  %s[%3d] go=%+9.6f lib=%+9.6f diff=%+9.6f ratio=%+.3f",
				sign, j, goSamples[j], libPcm[j], diff, ratio)
		}

		// Count inversions
		inversions := 0
		for j := 0; j < minLen; j++ {
			if goSamples[j] != 0 && libPcm[j] != 0 {
				r := goSamples[j] / libPcm[j]
				if r < -0.5 && r > -2 {
					inversions++
				}
			}
		}
		t.Logf("Sign inversions: %d / %d (%.1f%%)", inversions, minLen, 100.0*float64(inversions)/float64(minLen))
	}
}

// TestTV12RunToPacket137 shows cumulative SNR leading up to packet 137.
func TestTV12RunToPacket137(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Cumulative SNR to Packet 137 ===")

	var totalSqErr, totalSqSig float64

	for i := 0; i < 145 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goSamples, _ := decodeFloat32(goDec, pkt)
		libPcm, libN := libDec.DecodeFloat(pkt, len(goSamples)*2)

		minLen := len(goSamples)
		if libN < minLen {
			minLen = libN
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := goSamples[j] - libPcm[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j] * libPcm[j])
		}
		totalSqErr += sumSqErr
		totalSqSig += sumSqSig

		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		cumSnr := 10 * math.Log10(totalSqSig/totalSqErr)

		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		if math.IsNaN(cumSnr) || math.IsInf(cumSnr, 1) {
			cumSnr = 999.0
		}

		marker := ""
		if i == 137 {
			marker = " <-- BW CHANGE"
		}

		if i%10 == 0 || i >= 135 {
			t.Logf("Packet %3d: BW=%d pktSNR=%6.1f cumSNR=%6.1f%s",
				i, toc.Bandwidth, snr, cumSnr, marker)
		}
	}
}
