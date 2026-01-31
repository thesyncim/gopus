// Package cgo compares SILK decoding using single 48kHz output decoders.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12Single48kDecoders uses a single 48kHz decoder for the entire stream.
// This matches how the actual test vector comparison works.
func TestTV12Single48kDecoders(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create single 48kHz decoders
	goDec, _ := gopus.NewDecoderDefault(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Single 48kHz decoder comparison ===")

	prevBW := gopus.Bandwidth(255)

	for i := 0; i <= 828; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

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

		// Log bandwidth changes and failing packets
		isBWChange := toc.Bandwidth != prevBW
		prevBW = toc.Bandwidth

		if isBWChange || snr < 40 || (i >= 824 && i <= 828) {
			marker := ""
			if isBWChange {
				marker = " <-- BW CHANGE"
			}
			t.Logf("Packet %4d: BW=%d, SNR=%6.1f dB%s", i, toc.Bandwidth, snr, marker)

			// Show first samples for BW changes or failing packets
			if (isBWChange || snr < 40) && minLen > 0 {
				t.Log("  First 10 samples:")
				for j := 0; j < 10 && j < minLen; j++ {
					t.Logf("    [%2d] go=%+9.6f lib=%+9.6f diff=%+9.6f",
						j, goSamples[j], libPcm[j], goSamples[j]-libPcm[j])
				}
			}
		}
	}
}

// TestTV12BWTransitionPackets focuses on bandwidth transition packets.
func TestTV12BWTransitionPackets(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 1200)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Log("=== Finding all bandwidth transitions ===")

	// First pass: find all transitions
	var transitions []int
	prevBW := gopus.Bandwidth(255)
	for i := 0; i < len(packets); i++ {
		toc := gopus.ParseTOC(packets[i][0])
		if toc.Bandwidth != prevBW {
			transitions = append(transitions, i)
		}
		prevBW = toc.Bandwidth
	}
	t.Logf("Found %d bandwidth transitions", len(transitions))

	// Create decoders
	goDec, _ := gopus.NewDecoderDefault(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode all packets and track SNR at transitions
	t.Log("\nSNR at each bandwidth transition:")
	prevBW = gopus.Bandwidth(255)
	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
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

		// Report transition packets
		if toc.Bandwidth != prevBW {
			status := "PASS"
			if snr < 40 {
				status = "FAIL"
			}
			t.Logf("  Packet %4d: BW %d->%d, Mode=%v, SNR=%6.1f dB [%s]",
				i, prevBW, toc.Bandwidth, toc.Mode, snr, status)
		}
		prevBW = toc.Bandwidth
	}
}
