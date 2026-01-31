// Package cgo checks if SNR recovers after bandwidth transitions.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12BWRecovery checks SNR recovery after bandwidth transitions.
func TestTV12BWRecovery(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 1000)
	if err != nil {
		t.Skip("Could not load packets")
	}

	goDec, _ := gopus.NewDecoderDefault(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Checking SNR recovery after NB→MB→WB→MB→NB transitions ===")

	// Transition points from earlier analysis:
	// NB→MB at 137, MB→WB at 214, Hybrid at 386-607, WB→MB at 758, MB→NB at 826

	checkRanges := [][2]int{
		{134, 145}, // Around NB→MB at 137
		{210, 220}, // Around MB→WB at 214
		{755, 765}, // Around WB→MB at 758
		{823, 835}, // Around MB→NB at 826
	}

	for _, r := range checkRanges {
		start, end := r[0], r[1]
		if end > len(packets) {
			end = len(packets)
		}

		// Reset decoders
		goDec.Reset()
		libDec.Destroy()
		libDec, _ = NewLibopusDecoder(48000, 1)

		// Process packets 0 to end
		t.Logf("\n=== Packets %d-%d ===", start, end-1)
		for i := 0; i < end; i++ {
			pkt := packets[i]
			goOut, _ := decodeFloat32(goDec, pkt)
			libOut, libN := libDec.DecodeFloat(pkt, len(goOut)*2)

			if i >= start {
				toc := gopus.ParseTOC(pkt[0])

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

				bwNames := []string{"NB", "MB", "WB", "SWB", "FB"}
				marker := ""
				if snr < 20 {
					marker = " [LOW]"
				}
				t.Logf("Pkt %d: %s SNR=%.1f dB%s", i, bwNames[toc.Bandwidth], snr, marker)
			}
		}
	}
}
