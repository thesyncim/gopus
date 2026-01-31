package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

func TestTV12Packets210to220(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 225)
	if err != nil {
		t.Skip("Could not load packets")
	}

	goDec, _ := gopus.NewDecoderDefault(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Packets 210-220 (MB→WB transition at 214) ===")

	// Process packets 0-209
	for i := 0; i < 210; i++ {
		decodeFloat32(goDec, packets[i])
		libDec.DecodeFloat(packets[i], 1920)
	}

	// Analyze packets 210-220
	for i := 210; i < 220 && i < len(packets); i++ {
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
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		bwName := "NB"
		switch toc.Bandwidth {
		case 1:
			bwName = "MB"
		case 2:
			bwName = "WB"
		}

		marker := ""
		if i == 214 {
			marker = " <-- BW CHANGE MB→WB"
		}

		t.Logf("Packet %d: Mode=%v BW=%s SNR=%.1f dB%s", i, toc.Mode, bwName, snr, marker)
	}
}
