// Package cgo provides CGO comparison tests for TV12 48kHz output.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12Packet826_48kHz compares 48kHz output for packet 826.
// Native SILK output is correct; this tests if resampling introduces sign inversions.
func TestTV12Packet826_48kHz(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create 48kHz mono decoders
	goDec, err := gopus.NewDecoderDefault(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	targetIdx := 826

	// Decode all packets up to target to build state
	for pktIdx := 0; pktIdx <= targetIdx; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])

		// Decode with Go
		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
			t.Logf("Packet %d: Go decode error: %v", pktIdx, err)
			continue
		}

		// Decode with libopus
		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libSamples < 0 {
			t.Logf("Packet %d: libopus decode failed", pktIdx)
			continue
		}

		if pktIdx == targetIdx {
			t.Logf("\n=== Packet %d at 48kHz ===", pktIdx)
			t.Logf("Mode: %v, BW: %v, FrameSize: %d", toc.Mode, toc.Bandwidth, toc.FrameSize)
			t.Logf("Go samples: %d, Lib samples: %d", len(goSamples), libSamples)

			minLen := len(goSamples)
			if libSamples < minLen {
				minLen = libSamples
			}

			// Calculate SNR at 48kHz
			var sumSqErr, sumSqSig float64
			var maxDiff float32
			maxDiffIdx := 0
			signInversions := 0

			for i := 0; i < minLen; i++ {
				goVal := goSamples[i]
				libVal := libPcm[i]
				diff := goVal - libVal
				sumSqErr += float64(diff * diff)
				sumSqSig += float64(libVal * libVal)
				if diff < 0 {
					diff = -diff
				}
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
				// Count sign inversions
				if (goVal > 0.01 && libVal < -0.01) || (goVal < -0.01 && libVal > 0.01) {
					signInversions++
				}
			}
			snr := 10 * math.Log10(sumSqSig/sumSqErr)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999.0
			}

			t.Logf("48kHz SNR: %.1f dB, MaxDiff: %.4f at sample %d", snr, maxDiff, maxDiffIdx)
			t.Logf("Sign inversions: %d out of %d samples", signInversions, minLen)

			// Show samples around max diff
			t.Logf("\nSamples around max diff [%d]:", maxDiffIdx)
			start := maxDiffIdx - 20
			if start < 0 {
				start = 0
			}
			end := maxDiffIdx + 21
			if end > minLen {
				end = minLen
			}
			for i := start; i < end; i++ {
				marker := ""
				if i == maxDiffIdx {
					marker = " <-- MAX"
				}
				invMarker := ""
				if (goSamples[i] > 0.01 && libPcm[i] < -0.01) || (goSamples[i] < -0.01 && libPcm[i] > 0.01) {
					invMarker = " <-- SIGN INV"
				}
				t.Logf("  [%4d] go=%+9.6f lib=%+9.6f diff=%+9.6f%s%s",
					i, goSamples[i], libPcm[i], goSamples[i]-libPcm[i], marker, invMarker)
			}

			// Show first 40 samples
			t.Logf("\nFirst 40 samples at 48kHz:")
			for i := 0; i < 40 && i < minLen; i++ {
				invMarker := ""
				if (goSamples[i] > 0.01 && libPcm[i] < -0.01) || (goSamples[i] < -0.01 && libPcm[i] > 0.01) {
					invMarker = " <-- SIGN INV"
				}
				t.Logf("  [%4d] go=%+9.6f lib=%+9.6f diff=%+9.6f%s",
					i, goSamples[i], libPcm[i], goSamples[i]-libPcm[i], invMarker)
			}

			// Find first sign inversion
			t.Logf("\nFirst 10 sign inversions:")
			invCount := 0
			for i := 0; i < minLen && invCount < 10; i++ {
				if (goSamples[i] > 0.01 && libPcm[i] < -0.01) || (goSamples[i] < -0.01 && libPcm[i] > 0.01) {
					t.Logf("  [%4d] go=%+9.6f lib=%+9.6f", i, goSamples[i], libPcm[i])
					invCount++
				}
			}
		}
	}
}

// TestTV12WorstPackets48kHz compares 48kHz output for all worst SILK packets.
func TestTV12WorstPackets48kHz(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 1200)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create 48kHz mono decoders
	goDec, err := gopus.NewDecoderDefault(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Worst packets from analysis
	worstPackets := map[int]bool{826: true, 213: true, 137: true, 758: true, 1041: true, 1118: true}

	t.Log("48kHz comparison for worst SILK packets:")

	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])

		// Decode with Go
		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
			continue
		}

		// Decode with libopus
		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libSamples < 0 {
			continue
		}

		if !worstPackets[pktIdx] {
			continue
		}

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		var sumSqErr, sumSqSig float64
		var maxDiff float32
		signInversions := 0

		for i := 0; i < minLen; i++ {
			goVal := goSamples[i]
			libVal := libPcm[i]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
			}
			if (goVal > 0.01 && libVal < -0.01) || (goVal < -0.01 && libVal > 0.01) {
				signInversions++
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("Packet %4d: Mode=%v BW=%v SNR=%6.1f dB, MaxDiff=%.4f, SignInv=%d",
			pktIdx, toc.Mode, toc.Bandwidth, snr, maxDiff, signInversions)
	}
}
