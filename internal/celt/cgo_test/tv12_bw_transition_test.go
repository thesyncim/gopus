// Package cgo tests bandwidth transitions in TV12.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12BandwidthTransition focuses on packet 826 where BW changes from MB to NB.
// This tests whether sMid state handling on bandwidth change matches libopus.
func TestTV12BandwidthTransition(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create 48kHz mono decoders
	goDec, err := gopus.NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode packets 824-827 (around the BW transition)
	t.Log("=== Bandwidth Transition Analysis ===")
	t.Log("Packets 824-826: MB(12kHz)->NB(8kHz) transition at 826")

	for pktIdx := 820; pktIdx <= 828 && pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])

		goSamples, err := goDec.DecodeFloat32(pkt)
		if err != nil {
			t.Logf("Packet %d: Go error: %v", pktIdx, err)
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libSamples < 0 {
			t.Logf("Packet %d: lib error", pktIdx)
			continue
		}

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		// Calculate SNR
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

		marker := ""
		if pktIdx == 826 {
			marker = " <-- BW TRANSITION"
		}

		t.Logf("\nPacket %d: BW=%d, SNR=%.1f dB%s", pktIdx, toc.Bandwidth, snr, marker)

		// Show first 20 samples
		for i := 0; i < 20 && i < minLen; i++ {
			t.Logf("  [%3d] go=%+9.6f lib=%+9.6f diff=%+9.6f",
				i, goSamples[i], libPcm[i], goSamples[i]-libPcm[i])
		}

		// Show where samples diverge significantly
		if snr < 40 {
			t.Logf("  First divergence points:")
			divergeCount := 0
			for i := 0; i < minLen && divergeCount < 5; i++ {
				diff := goSamples[i] - libPcm[i]
				if diff < 0 {
					diff = -diff
				}
				if diff > 0.002 {
					t.Logf("    [%3d] go=%+9.6f lib=%+9.6f diff=%+9.6f",
						i, goSamples[i], libPcm[i], goSamples[i]-libPcm[i])
					divergeCount++
				}
			}
		}
	}
}

// TestTV12AllBandwidthTransitions tests all packets at bandwidth transitions.
func TestTV12AllBandwidthTransitions(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 1200)
	if err != nil {
		t.Skip("Could not load packets")
	}

	goDec, err := gopus.NewDecoder(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Track bandwidth transitions
	prevBW := gopus.Bandwidth(255)
	transitionPackets := []int{}

	for i := 0; i < len(packets); i++ {
		toc := gopus.ParseTOC(packets[i][0])
		if toc.Bandwidth != prevBW {
			transitionPackets = append(transitionPackets, i)
			prevBW = toc.Bandwidth
		}
	}

	t.Logf("Found %d bandwidth transitions: %v", len(transitionPackets), transitionPackets)
	t.Log("\nSNR at each bandwidth transition:")

	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goSamples, err := goDec.DecodeFloat32(pkt)
		if err != nil {
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libSamples < 0 {
			continue
		}

		// Check if this is a transition packet
		isTransition := false
		for _, tp := range transitionPackets {
			if i == tp {
				isTransition = true
				break
			}
		}
		if !isTransition {
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

		status := "PASS"
		if snr < 40 {
			status = "FAIL"
		}
		t.Logf("  Packet %4d: Mode=%v BW=%d SNR=%6.1f dB %s",
			i, toc.Mode, toc.Bandwidth, snr, status)
	}
}
