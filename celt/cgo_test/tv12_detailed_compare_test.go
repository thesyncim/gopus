//go:build cgo_libopus
// +build cgo_libopus

// Package cgo does detailed comparison of TV12 outputs
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12DetailedCompare compares full Opus decoder output at 48kHz.
func TestTV12DetailedCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 1400)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder at 48kHz mono (TV12 is mono)
	goDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	// Create libopus decoder at 48kHz mono
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Track bandwidth transitions
	prevBW := -1
	var worstPacket int
	worstSNR := 999.0

	// Compare each packet
	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		bw := int(toc.Bandwidth)

		// Decode with gopus
		goOut, err := decodeFloat32(goDec, pkt)
		if err != nil {
			t.Logf("Packet %d: gopus decode error: %v", i, err)
			continue
		}

		// Decode with libopus
		libOut, libSamples := libDec.DecodeFloat(pkt, 1920)

		// Compare
		minLen := len(goOut)
		if libSamples < minLen {
			minLen = libSamples
		}

		var sumSqErr, sumSqSig float64
		var maxDiff float32
		maxDiffIdx := 0

		for j := 0; j < minLen; j++ {
			diff := goOut[j] - libOut[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libOut[j] * libOut[j])
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
				maxDiffIdx = j
			}
		}

		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Track bandwidth changes
		bwChanged := (prevBW >= 0) && (bw != prevBW)
		prevBW = bw

		// Track worst packet
		if snr < worstSNR {
			worstSNR = snr
			worstPacket = i
		}

		// Report bad packets and transitions
		if snr < 20 || bwChanged {
			modeStr := "SILK"
			if toc.Mode == gopus.ModeHybrid {
				modeStr = "Hybrid"
			} else if toc.Mode == gopus.ModeCELT {
				modeStr = "CELT"
			}
			bwStr := ""
			if bwChanged {
				bwStr = " [BW CHANGE]"
			}
			t.Logf("Packet %4d: Mode=%s BW=%d, SNR=%.1f dB, MaxDiff=%.6f @ %d%s",
				i, modeStr, bw, snr, maxDiff, maxDiffIdx, bwStr)
		}

		// Stop after processing all packets
		if i >= len(packets)-1 {
			break
		}
	}

	t.Logf("\nWorst packet: %d with SNR=%.2f dB", worstPacket, worstSNR)
}
