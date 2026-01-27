// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestCELTMonoDivergence investigates where CELT mono diverges from libopus.
func TestCELTMonoDivergence(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"

	packets, err := loadPacketsSimple(bitFile, 50) // First 50 packets
	if err != nil {
		t.Skip("Could not load packets:", err)
	}

	toc := gopus.ParseTOC(packets[0][0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}

	t.Logf("CELT mono test: channels=%d, mode=%d", channels, toc.Mode)

	// Create decoders
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	firstDivergePkt := -1

	for i, pkt := range packets {
		pktTOC := gopus.ParseTOC(pkt[0])

		// Decode with Go
		goOut, err := goDec.DecodeFloat32(pkt)
		if err != nil {
			t.Logf("Packet %d: Go error: %v", i, err)
			continue
		}

		// Decode with libopus
		libOut, libN := libDec.DecodeFloat(pkt, 5760)
		if libN <= 0 {
			t.Logf("Packet %d: libopus error", i)
			continue
		}
		libTotal := libN * channels

		if len(goOut) != libTotal {
			t.Logf("Packet %d: length mismatch go=%d lib=%d", i, len(goOut), libTotal)
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

		if snr < 80 && firstDivergePkt < 0 {
			firstDivergePkt = i
			t.Logf("FIRST DIVERGENCE at packet %d: SNR=%.2f dB", i, snr)

			// Show first few samples
			t.Log("First 10 samples:")
			for j := 0; j < 10 && j < libTotal; j++ {
				t.Logf("  [%d] go=%.6f lib=%.6f diff=%.6f",
					j, goOut[j], libOut[j], goOut[j]-libOut[j])
			}

			// Show samples around 480 (mid frame)
			if libTotal > 490 {
				t.Log("Samples around 480:")
				for j := 475; j < 485; j++ {
					t.Logf("  [%d] go=%.6f lib=%.6f diff=%.6f",
						j, goOut[j], libOut[j], goOut[j]-libOut[j])
				}
			}
		}

		status := "OK"
		if snr < 80 {
			status = "DIVERGE"
		}

		t.Logf("Packet %d: SNR=%.2f dB %s (mode=%d fs=%d)",
			i, snr, status, pktTOC.Mode, pktTOC.FrameSize)
	}

	if firstDivergePkt >= 0 {
		t.Logf("\nDivergence starts at packet %d", firstDivergePkt)
	} else {
		t.Log("\nAll packets match!")
	}
}
