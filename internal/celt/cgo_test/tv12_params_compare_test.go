// Package cgo compares SILK decoder parameters for packet 826.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Packet826DetailedCompare performs detailed comparison at packet 826.
// This test uses Opus-level decoders for proper state evolution.
func TestTV12Packet826DetailedCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create Opus-level decoders
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== TV12 Packet 826 Detailed Comparison ===")

	// Process packets 0-825 through both decoders
	t.Log("Processing packets 0-825...")
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		decodeFloat32(goDec, pkt)
		libDec.DecodeFloat(pkt, 1920)
	}

	// Decode packets 826-828 and compare
	for pktIdx := 826; pktIdx <= 828; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])

		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		config := silk.GetBandwidthConfig(silkBW)

		goOut, _ := decodeFloat32(goDec, pkt)
		libOut, libN := libDec.DecodeFloat(pkt, 1920)

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

		t.Logf("\nPacket %d: BW=%v (%dkHz), Mode=%v, SNR=%.1f dB",
			pktIdx, toc.Bandwidth, config.SampleRate/1000, toc.Mode, snr)

		if pktIdx == 826 {
			t.Log("First 30 samples at 48kHz:")
			for j := 0; j < 30 && j < minLen; j++ {
				ratio := float32(0)
				if libOut[j] != 0 {
					ratio = goOut[j] / libOut[j]
				}
				t.Logf("  [%2d] go=%+9.6f lib=%+9.6f diff=%+9.6f ratio=%.3f",
					j, goOut[j], libOut[j], goOut[j]-libOut[j], ratio)
			}

			// Check where they converge
			convergeIdx := -1
			for j := 0; j < minLen; j++ {
				diff := math.Abs(float64(goOut[j] - libOut[j]))
				if diff < 0.0001 {
					convergeIdx = j
					break
				}
			}
			if convergeIdx >= 0 {
				t.Logf("\nFirst convergence (diff < 0.0001) at sample %d", convergeIdx)
			}
		}
	}
}
