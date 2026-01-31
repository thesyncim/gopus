// Package cgo checks SILK state after Hybrid packets.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12HybridTransition checks SNR around Hybrid→SILK transitions.
func TestTV12HybridTransition(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	goDec, _ := gopus.NewDecoder(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Checking SNR around mode transitions ===")

	// Track mode changes and SNR
	prevMode := gopus.ModeSILK
	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goOut, _ := goDec.DecodeFloat32(pkt)
		libOut, libN := libDec.DecodeFloat(pkt, len(goOut)*2)

		minLen := len(goOut)
		if libN < minLen {
			minLen = libN
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := goOut[j] - libOut[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libOut[j] * libOut[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Print at mode transitions and nearby packets
		modeChanged := toc.Mode != prevMode
		if modeChanged || snr < 20 {
			modeStr := ""
			switch toc.Mode {
			case gopus.ModeSILK:
				modeStr = "SILK"
			case gopus.ModeHybrid:
				modeStr = "Hybrid"
			case gopus.ModeCELT:
				modeStr = "CELT"
			}
			bwStr := ""
			switch toc.Bandwidth {
			case 0:
				bwStr = "NB"
			case 1:
				bwStr = "MB"
			case 2:
				bwStr = "WB"
			case 3:
				bwStr = "SWB"
			case 4:
				bwStr = "FB"
			}

			marker := ""
			if modeChanged {
				marker = " <-- MODE CHANGE"
			}
			if snr < 20 {
				marker += " [LOW SNR]"
			}
			t.Logf("Pkt %4d: %s %s SNR=%.1f dB%s", i, modeStr, bwStr, snr, marker)
		}

		prevMode = toc.Mode
	}
}
