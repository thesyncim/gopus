// Package cgo tests the full Opus decoder for TV12.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12OpusLevelComparison tests the full Opus decoder flow.
func TestTV12OpusLevelComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create Opus-level decoders
	goDec, _ := gopus.NewDecoderDefault(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Opus-level decode comparison ===")

	// Track bandwidth transitions
	prevBW := gopus.Bandwidth(255)

	for i := 0; i <= 828; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Log bandwidth transitions
		if toc.Bandwidth != prevBW {
			t.Logf("BW transition at packet %d: %d -> %d", i, prevBW, toc.Bandwidth)
		}
		prevBW = toc.Bandwidth

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

		// Report packets around 826 and any failing packets
		if (i >= 824 && i <= 828) || snr < 40 {
			t.Logf("Packet %d: BW=%d, Mode=%v, SNR=%.1f dB, goLen=%d, libLen=%d",
				i, toc.Bandwidth, toc.Mode, snr, len(goSamples), libSamples)

			// For packet 826, show sample differences
			if i == 826 && snr < 40 {
				t.Log("First 20 samples of packet 826:")
				for j := 0; j < 20 && j < minLen; j++ {
					t.Logf("  [%3d] go=%+9.6f lib=%+9.6f diff=%+9.6f",
						j, goSamples[j], libPcm[j], goSamples[j]-libPcm[j])
				}
			}
		}
	}
}

// TestTV12NativeSilkOnly tests SILK decoding at native rate with the Opus decoder.
func TestTV12NativeSilkOnly(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create decoders at 8kHz to get SILK output without resampling
	goDec8k, _ := gopus.NewDecoderDefault(8000, 1)
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	if libDec8k == nil {
		t.Skip("Could not create 8k libopus decoder")
	}
	defer libDec8k.Destroy()

	// Also 12k for MB packets
	goDec12k, _ := gopus.NewDecoderDefault(12000, 1)
	libDec12k, _ := NewLibopusDecoder(12000, 1)
	if libDec12k == nil {
		t.Skip("Could not create 12k libopus decoder")
	}
	defer libDec12k.Destroy()

	t.Log("=== SILK decode at native rate ===")

	for i := 824; i <= 828 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		// Choose decoder based on bandwidth
		var goDec *gopus.Decoder
		var libDec *LibopusDecoder
		var rate int
		if toc.Bandwidth == 0 { // NB = 8kHz
			goDec = goDec8k
			libDec = libDec8k
			rate = 8000
		} else { // MB = 12kHz
			goDec = goDec12k
			libDec = libDec12k
			rate = 12000
		}

		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
			t.Logf("Packet %d: gopus error: %v", i, err)
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)

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

		t.Logf("Packet %d: BW=%d (%dHz), SNR=%.1f dB, goLen=%d, libLen=%d",
			i, toc.Bandwidth, rate, snr, len(goSamples), libSamples)

		if snr < 40 {
			t.Log("First 20 samples:")
			for j := 0; j < 20 && j < minLen; j++ {
				t.Logf("  [%3d] go=%+9.6f lib=%+9.6f diff=%+9.6f",
					j, goSamples[j], libPcm[j], goSamples[j]-libPcm[j])
			}
		}
	}
}
