// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestSILKBandwidthCompare compares first packet of NB vs MB vs WB.
func TestSILKBandwidthCompare(t *testing.T) {
	tests := []struct {
		name   string
		vector string
	}{
		{"NB (works)", "testvector02"},
		{"MB (fails)", "testvector03"},
		{"WB (fails)", "testvector04"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + tt.vector + ".bit"

			packets, err := loadPacketsSimple(bitFile, 5)
			if err != nil {
				t.Skip("Could not load packets")
			}

			toc := gopus.ParseTOC(packets[0][0])
			t.Logf("TOC: mode=%d, bw=%d, stereo=%v, frameSize=%d",
				toc.Mode, toc.Bandwidth, toc.Stereo, toc.FrameSize)

			// Create decoders
			goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
			libDec, _ := NewLibopusDecoder(48000, 1)
			if libDec == nil {
				t.Skip("Could not create libopus decoder")
			}
			defer libDec.Destroy()

			// Decode packet 0
			pkt := packets[0]
			goOut, err := decodeFloat32(goDec, pkt)
			if err != nil {
				t.Fatalf("Go decode error: %v", err)
			}

			libOut, libN := libDec.DecodeFloat(pkt, 5760)
			if libN <= 0 {
				t.Fatalf("libopus decode error")
			}

			t.Logf("Go output: %d samples", len(goOut))
			t.Logf("Lib output: %d samples", libN)

			// Calculate SNR
			minLen := len(goOut)
			if libN < minLen {
				minLen = libN
			}

			var noise, signal float64
			exactMatches := 0
			for i := 0; i < minLen; i++ {
				diff := float64(goOut[i]) - float64(libOut[i])
				noise += diff * diff
				signal += float64(libOut[i]) * float64(libOut[i])
				if diff == 0 || (diff > -1e-7 && diff < 1e-7) {
					exactMatches++
				}
			}

			snr := 10 * math.Log10(signal/noise)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999.0
			}

			pct := 100.0 * float64(exactMatches) / float64(minLen)
			t.Logf("SNR: %.2f dB, exact: %.1f%%", snr, pct)

			// Show first 20 samples
			t.Log("First 20 samples:")
			for i := 0; i < 20 && i < minLen; i++ {
				diff := goOut[i] - libOut[i]
				matchStr := ""
				if diff == 0 || (diff > -1e-7 && diff < 1e-7) {
					matchStr = " ✓"
				}
				t.Logf("  [%d] go=%.6f lib=%.6f diff=%.9f%s",
					i, goOut[i], libOut[i], diff, matchStr)
			}

			// Show samples around frame boundary (480 samples at 48kHz for NB = 160 at 8kHz, etc.)
			if minLen > 500 {
				t.Log("\nSamples around 480:")
				for i := 478; i < 485 && i < minLen; i++ {
					diff := goOut[i] - libOut[i]
					t.Logf("  [%d] go=%.6f lib=%.6f diff=%.9f",
						i, goOut[i], libOut[i], diff)
				}
			}
		})
	}
}
