// Package cgo debugs TV12 bandwidth transition at packet 137
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12BWTransitionDebug traces packets around the first BW transition
func TestTV12BWTransitionDebug(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 150)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder at 48kHz mono
	goDec, err := gopus.NewDecoder(48000, 1)
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	// Create libopus decoder at 48kHz mono
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("Decoding packets 130-145 to find first BW transition...")

	prevBW := -1
	for i := 130; i < 145 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		bw := int(toc.Bandwidth)
		modeStr := "SILK"
		if toc.Mode == gopus.ModeHybrid {
			modeStr = "Hybrid"
		} else if toc.Mode == gopus.ModeCELT {
			modeStr = "CELT"
		}

		// Decode with gopus
		goOut, err := goDec.DecodeFloat32(pkt)
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

		bwChanged := prevBW >= 0 && bw != prevBW
		prevBW = bw

		bwStr := ""
		if bwChanged {
			bwStr = " [BW CHANGE]"
		}

		// Always log for these specific packets
		t.Logf("Packet %3d: Mode=%s BW=%d, SNR=%.1f dB, MaxDiff=%.6f @ %d, samples=%d%s",
			i, modeStr, bw, snr, maxDiff, maxDiffIdx, len(goOut), bwStr)

		// For packets with bad SNR, dump first 10 samples
		if snr < 20 {
			t.Log("  First 10 samples comparison:")
			for j := 0; j < 10 && j < minLen; j++ {
				t.Logf("    [%3d] go=%+.6f lib=%+.6f diff=%+.6f",
					j, goOut[j], libOut[j], goOut[j]-libOut[j])
			}
			t.Log("  Samples around max diff:")
			start := maxDiffIdx - 3
			if start < 0 {
				start = 0
			}
			end := maxDiffIdx + 5
			if end > minLen {
				end = minLen
			}
			for j := start; j < end; j++ {
				marker := ""
				if j == maxDiffIdx {
					marker = " <-- MAX"
				}
				t.Logf("    [%3d] go=%+.6f lib=%+.6f diff=%+.6f%s",
					j, goOut[j], libOut[j], goOut[j]-libOut[j], marker)
			}
		}
	}
}
