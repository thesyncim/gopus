// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkAllPacketsAligned tests all packets with proper delay alignment.
func TestSilkAllPacketsAligned(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Logf("Testing %d packets with fresh decoders:", len(packets))

	signalTypes := []string{"inactive", "unvoiced", "voiced"}

	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			t.Logf("Packet %2d: Not SILK mode", pktIdx)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		delay := 5
		if config.SampleRate == 16000 {
			delay = 13
		} else if config.SampleRate == 12000 {
			delay = 10
		}

		// Fresh decoders
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goDec := silk.NewDecoder()
		goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			t.Logf("Packet %2d: gopus error: %v", pktIdx, err)
			continue
		}

		sigType := goDec.GetLastSignalType()
		sigName := signalTypes[sigType]

		libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
		if libDec == nil {
			continue
		}
		libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
		libDec.Destroy()
		if libSamples < 0 {
			continue
		}

		alignedLen := len(goNative)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}

		var sumSqErr, sumSqSig float64
		exactMatches := 0
		for i := 0; i < alignedLen; i++ {
			goVal := goNative[i]
			libVal := libPcm[i+delay]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff == 0 {
				exactMatches++
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		status := "PASS"
		if snr < 999 {
			status = "FAIL"
		}

		t.Logf("Packet %2d %-9s: aligned SNR=%6.1f dB, exact=%5.1f%% %s",
			pktIdx, sigName, snr, 100.0*float64(exactMatches)/float64(alignedLen), status)
	}
}
