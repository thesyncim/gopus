// Package cgo tests packet 825 which precedes the bandwidth transition.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12Packet825 looks at packet 825 which has SNR=11.9dB but first samples match.
func TestTV12Packet825(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	goDec, err := gopus.NewDecoderDefault(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode all packets up to 825
	for pktIdx := 0; pktIdx <= 825 && pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]

		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libSamples < 0 {
			continue
		}

		if pktIdx == 825 {
			toc := gopus.ParseTOC(pkt[0])
			t.Logf("Packet 825: BW=%d, FrameSize=%d", toc.Bandwidth, toc.FrameSize)

			minLen := len(goSamples)
			if libSamples < minLen {
				minLen = libSamples
			}

			var sumSqErr, sumSqSig float64
			var maxDiff float32
			maxDiffIdx := 0

			for i := 0; i < minLen; i++ {
				diff := goSamples[i] - libPcm[i]
				sumSqErr += float64(diff * diff)
				sumSqSig += float64(libPcm[i] * libPcm[i])
				if diff < 0 {
					diff = -diff
				}
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
			}
			snr := 10 * math.Log10(sumSqSig/sumSqErr)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999.0
			}

			t.Logf("SNR=%.1f dB, MaxDiff=%.6f at sample %d", snr, maxDiff, maxDiffIdx)

			// Show samples around max diff
			t.Logf("\nSamples around max diff [%d]:", maxDiffIdx)
			start := maxDiffIdx - 10
			if start < 0 {
				start = 0
			}
			end := maxDiffIdx + 11
			if end > minLen {
				end = minLen
			}
			for i := start; i < end; i++ {
				marker := ""
				if i == maxDiffIdx {
					marker = " <-- MAX"
				}
				t.Logf("  [%4d] go=%+9.6f lib=%+9.6f diff=%+9.6f%s",
					i, goSamples[i], libPcm[i], goSamples[i]-libPcm[i], marker)
			}

			// Find where divergence starts (diff > 0.001)
			t.Logf("\nFirst divergence (diff > 0.001):")
			divergeCount := 0
			for i := 0; i < minLen && divergeCount < 5; i++ {
				diff := goSamples[i] - libPcm[i]
				if diff < 0 {
					diff = -diff
				}
				if diff > 0.001 {
					t.Logf("  [%4d] go=%+9.6f lib=%+9.6f diff=%+9.6f",
						i, goSamples[i], libPcm[i], goSamples[i]-libPcm[i])
					divergeCount++
				}
			}
		}
	}
}
