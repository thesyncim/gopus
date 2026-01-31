// Package cgo provides detailed native rate comparison
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12NativeRateDetailed compares SILK output at native rate with detailed sample dump.
func TestTV12NativeRateDetailed(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Create libopus decoder at NATIVE rate for packet 826 (which is NB = 8kHz)
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	if libDec8k == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec8k.Destroy()

	// Process all SILK packets up to 826
	for i := 0; i <= 826; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Decode with gopus SILK decoder (native rate)
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			continue
		}

		// Only for packet 826
		if i != 826 {
			// Keep libopus in sync
			libDec8k.DecodeFloat(pkt, len(goNative)*2)
			continue
		}

		// Decode with libopus at native rate
		libPcm, libSamples := libDec8k.DecodeFloat(pkt, len(goNative)*2)

		minLen := len(goNative)
		if libSamples < minLen {
			minLen = libSamples
		}

		// Calculate SNR
		var sumSqErr, sumSqSig float64
		var maxDiff float32
		maxDiffIdx := 0
		signInversions := 0

		for j := 0; j < minLen; j++ {
			diff := goNative[j] - libPcm[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j] * libPcm[j])
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
				maxDiffIdx = j
			}
			// Count sign inversions (even small values)
			if (goNative[j] > 0.0001 && libPcm[j] < -0.0001) || (goNative[j] < -0.0001 && libPcm[j] > 0.0001) {
				signInversions++
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("\n=== Packet 826 at NATIVE rate (8kHz) ===")
		t.Logf("Go samples: %d, Lib samples: %d", len(goNative), libSamples)
		t.Logf("SNR: %.1f dB, MaxDiff: %.6f at sample %d", snr, maxDiff, maxDiffIdx)
		t.Logf("Sign inversions: %d", signInversions)

		// Dump first 40 samples
		t.Logf("\nFirst 40 samples at NATIVE 8kHz:")
		for j := 0; j < 40 && j < minLen; j++ {
			marker := ""
			if j == maxDiffIdx {
				marker = " <-- MAX"
			}
			invMarker := ""
			if (goNative[j] > 0.0001 && libPcm[j] < -0.0001) || (goNative[j] < -0.0001 && libPcm[j] > 0.0001) {
				invMarker = " <-- SIGN INV"
			}
			t.Logf("  [%4d] go=%+9.6f lib=%+9.6f diff=%+9.6f%s%s",
				j, goNative[j], libPcm[j], goNative[j]-libPcm[j], marker, invMarker)
		}

		// Dump samples around max diff
		t.Logf("\nSamples around max diff [%d]:", maxDiffIdx)
		start := maxDiffIdx - 10
		if start < 0 {
			start = 0
		}
		end := maxDiffIdx + 11
		if end > minLen {
			end = minLen
		}
		for j := start; j < end; j++ {
			marker := ""
			if j == maxDiffIdx {
				marker = " <-- MAX"
			}
			t.Logf("  [%4d] go=%+9.6f lib=%+9.6f diff=%+9.6f%s",
				j, goNative[j], libPcm[j], goNative[j]-libPcm[j], marker)
		}

		// Find first sign inversion if any
		for j := 0; j < minLen; j++ {
			if (goNative[j] > 0.0001 && libPcm[j] < -0.0001) || (goNative[j] < -0.0001 && libPcm[j] > 0.0001) {
				t.Logf("\nFirst sign inversion at sample %d:", j)
				s := j - 5
				if s < 0 {
					s = 0
				}
				e := j + 6
				if e > minLen {
					e = minLen
				}
				for k := s; k < e; k++ {
					marker := ""
					if k == j {
						marker = " <-- FIRST SIGN INV"
					}
					t.Logf("  [%4d] go=%+9.6f lib=%+9.6f%s", k, goNative[k], libPcm[k], marker)
				}
				break
			}
		}
	}
}
