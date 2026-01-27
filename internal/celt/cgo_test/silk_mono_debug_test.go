// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestSILKMonoDivergence investigates where SILK mono diverges from libopus.
func TestSILKMonoDivergence(t *testing.T) {
	vectors := []string{"testvector02", "testvector03", "testvector04", "testvector12"}

	for _, vectorName := range vectors {
		t.Run(vectorName, func(t *testing.T) {
			bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + vectorName + ".bit"

			packets, err := loadPacketsSimple(bitFile, 100)
			if err != nil {
				t.Skip("Could not load packets:", err)
			}

			toc := gopus.ParseTOC(packets[0][0])
			channels := 1
			if toc.Stereo {
				channels = 2
			}

			// Create decoders
			goDec, _ := gopus.NewDecoder(48000, channels)
			libDec, _ := NewLibopusDecoder(48000, channels)
			if libDec == nil {
				t.Skip("Could not create libopus decoder")
			}
			defer libDec.Destroy()

			lowSNRCount := 0
			highSNRCount := 0
			firstLowPkt := -1

			for i, pkt := range packets {
				pktTOC := gopus.ParseTOC(pkt[0])

				goOut, err := goDec.DecodeFloat32(pkt)
				if err != nil {
					continue
				}

				libOut, libN := libDec.DecodeFloat(pkt, 5760)
				if libN <= 0 {
					continue
				}
				libTotal := libN * channels

				if len(goOut) != libTotal {
					continue
				}

				// Calculate SNR
				var noise, signal float64
				for j := 0; j < libTotal; j++ {
					diff := float64(goOut[j]) - float64(libOut[j])
					noise += diff * diff
					signal += float64(libOut[j]) * float64(libOut[j])
				}

				snr := 10 * math.Log10(signal/noise)
				if math.IsNaN(snr) || math.IsInf(snr, 1) {
					snr = 999.0
				}

				if snr < 80 {
					lowSNRCount++
					if firstLowPkt < 0 {
						firstLowPkt = i
						t.Logf("FIRST LOW SNR at packet %d: %.2f dB (mode=%d fs=%d)",
							i, snr, pktTOC.Mode, pktTOC.FrameSize)
					}
				} else {
					highSNRCount++
				}
			}

			t.Logf("Summary: %d/%d packets with SNR >= 80 dB, first low at packet %d",
				highSNRCount, highSNRCount+lowSNRCount, firstLowPkt)
		})
	}
}

// TestSILK02Detailed does a more detailed analysis of testvector02.
func TestSILK02Detailed(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"

	packets, err := loadPacketsSimple(bitFile, 50)
	if err != nil {
		t.Skip("Could not load packets:", err)
	}

	goDec, _ := gopus.NewDecoder(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	for i, pkt := range packets {
		goOut, _ := goDec.DecodeFloat32(pkt)
		libOut, libN := libDec.DecodeFloat(pkt, 5760)
		if libN <= 0 || len(goOut) != libN {
			continue
		}

		// Calculate SNR
		var noise, signal float64
		exactMatches := 0
		for j := 0; j < libN; j++ {
			diff := float64(goOut[j]) - float64(libOut[j])
			noise += diff * diff
			signal += float64(libOut[j]) * float64(libOut[j])
			if diff == 0 || (diff > -1e-7 && diff < 1e-7) {
				exactMatches++
			}
		}

		snr := 10 * math.Log10(signal/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		pct := 100.0 * float64(exactMatches) / float64(libN)
		t.Logf("Packet %d: SNR=%.2f dB, exact=%.1f%%", i, snr, pct)
	}
}
