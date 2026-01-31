// Package cgo compares resampler internals between gopus and libopus
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResamplerIntermediateValues compares resampler behavior
// by checking output at specific sample indices.
func TestTV12ResamplerIntermediateValues(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 140)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder at 48kHz mono
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

	t.Log("=== Tracing packets 135-138 in detail ===\n")

	// Decode packets 0-134 to build state
	for i := 0; i < 135; i++ {
		pkt := packets[i]
		decodeFloat32(goDec, pkt)
		libDec.DecodeFloat(pkt, 1920)
	}

	// Now trace packets 135-138 in detail
	for i := 135; i < 139 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		bw := int(toc.Bandwidth)
		modeStr := "SILK"
		if toc.Mode == gopus.ModeHybrid {
			modeStr = "Hybrid"
		} else if toc.Mode == gopus.ModeCELT {
			modeStr = "CELT"
		}

		bwName := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}[bw]

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

		t.Logf("\n=== Packet %d: %s %s, %d samples ===", i, modeStr, bwName, len(goOut))
		t.Logf("SNR=%.1f dB, MaxDiff=%.6f @ sample %d", snr, maxDiff, maxDiffIdx)

		// Show first 10 samples
		t.Log("First 10 samples:")
		for j := 0; j < 10 && j < minLen; j++ {
			diffVal := goOut[j] - libOut[j]
			t.Logf("  [%3d] go=%+.6f lib=%+.6f diff=%+.6f", j, goOut[j], libOut[j], diffVal)
		}

		// Show samples around 48-52 (transition from first batch to second)
		if minLen > 55 {
			t.Log("Samples around batch boundary (48-55):")
			for j := 45; j < 55; j++ {
				diffVal := goOut[j] - libOut[j]
				marker := ""
				if j == 48 {
					marker = " <-- batch boundary"
				}
				t.Logf("  [%3d] go=%+.6f lib=%+.6f diff=%+.6f%s", j, goOut[j], libOut[j], diffVal, marker)
			}
		}

		// Show samples around max diff
		if maxDiff > 0.001 && maxDiffIdx > 5 && maxDiffIdx < minLen-5 {
			t.Log("Samples around max diff:")
			for j := maxDiffIdx - 3; j <= maxDiffIdx+3 && j < minLen; j++ {
				if j < 0 {
					continue
				}
				diffVal := goOut[j] - libOut[j]
				marker := ""
				if j == maxDiffIdx {
					marker = " <-- MAX"
				}
				t.Logf("  [%3d] go=%+.6f lib=%+.6f diff=%+.6f%s", j, goOut[j], libOut[j], diffVal, marker)
			}
		}

		// Check sign flips
		var signFlips int
		var firstSignFlip int = -1
		for j := 0; j < minLen; j++ {
			goSign := goOut[j] > 0
			libSign := libOut[j] > 0
			if goSign != libSign && math.Abs(float64(goOut[j])) > 0.001 && math.Abs(float64(libOut[j])) > 0.001 {
				signFlips++
				if firstSignFlip < 0 {
					firstSignFlip = j
				}
			}
		}
		if signFlips > 0 {
			t.Logf("Sign flips: %d (first at sample %d)", signFlips, firstSignFlip)
		}
	}
}

// TestTV12SILKNativeVsResampled compares SILK native output with resampled output
func TestTV12SILKNativeVsResampled(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 140)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create standalone SILK decoder
	silkDec := silk.NewDecoder()

	t.Log("=== Comparing SILK native vs resampled for packets 135-138 ===\n")

	// Process packets 0-134 to build state
	for i := 0; i < 135; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	}

	// Now trace packets 135-138
	for i := 135; i < 139 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			t.Logf("Packet %d: non-SILK mode, skipping", i)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			t.Logf("Packet %d: invalid bandwidth, skipping", i)
			continue
		}

		bwName := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}[silkBW]
		config := silk.GetBandwidthConfig(silkBW)

		// Decode
		resampledOut, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
			continue
		}

		t.Logf("\n=== Packet %d: %s, native %dHz → 48kHz ===", i, bwName, config.SampleRate)
		t.Logf("Resampled output: %d samples", len(resampledOut))

		// Show resampled output around sample 121
		if len(resampledOut) > 130 {
			t.Log("Resampled output [115-130]:")
			for j := 115; j < 130 && j < len(resampledOut); j++ {
				t.Logf("  [%3d] %+.6f", j, resampledOut[j])
			}
		}
	}
}
