// Package cgo tests TV02 at 48kHz (resampled).
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV02At48kHz tests SILK decoder against libopus at 48kHz.
func TestTV02At48kHz(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"

	packets, err := loadPacketsSimple(bitFile, 100)
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

	t.Log("=== TV02 at 48kHz Comparison ===")

	var totalSqErr, totalSqSig float64
	failCount := 0

	for i := 0; i < len(packets) && i < 100; i++ {
		pkt := packets[i]

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

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := goSamples[j] - libPcm[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j] * libPcm[j])
		}
		totalSqErr += sumSqErr
		totalSqSig += sumSqSig

		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Only log first 10 and bad packets
		if i < 10 || snr < 40 {
			status := "OK"
			if snr < 40 {
				status = "BAD"
				failCount++
			}
			t.Logf("Packet %3d: SNR=%6.1f dB [%s]", i, snr, status)

			if snr < 40 && failCount <= 3 {
				t.Log("  First 5 samples:")
				for j := 0; j < 5 && j < minLen; j++ {
					t.Logf("    [%2d] go=%+10.6f lib=%+10.6f diff=%+10.6f",
						j, goSamples[j], libPcm[j], goSamples[j]-libPcm[j])
				}
			}
		}
	}

	// Calculate overall SNR
	overallSNR := 10 * math.Log10(totalSqSig/totalSqErr)
	Q := (overallSNR - 48) * (100.0 / 48.0)
	t.Logf("\nOverall: SNR=%.2f dB, Q=%.2f", overallSNR, Q)
	if Q >= 0 {
		t.Log("PASS: Q >= 0")
	} else {
		t.Errorf("FAIL: Q = %.2f < 0", Q)
	}
}
