// Package cgo investigates SILK stereo decoding issues (testvector08/09)
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestSilkStereoAnalysis analyzes SILK stereo decoding issues
func TestSilkStereoAnalysis(t *testing.T) {
	// Test both testvector08 and testvector09 which have identical Q=-84.64
	testVectors := []string{
		"testvector08.bit",
		"testvector09.bit",
	}

	for _, vec := range testVectors {
		t.Run(vec, func(t *testing.T) {
			bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + vec
			data, err := os.ReadFile(bitFile)
			if err != nil {
				t.Skipf("Cannot read %s: %v", bitFile, err)
				return
			}

			var packets [][]byte
			offset := 0
			for offset < len(data)-8 {
				pktLen := binary.BigEndian.Uint32(data[offset:])
				offset += 4
				offset += 4
				if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
					break
				}
				packets = append(packets, data[offset:offset+int(pktLen)])
				offset += int(pktLen)
			}

			t.Logf("Total packets: %d", len(packets))

			// Analyze first packet to understand structure
			if len(packets) > 0 {
				toc := gopus.ParseTOC(packets[0][0])
				t.Logf("First packet: TOC=0x%02X mode=%d stereo=%v fs=%d",
					packets[0][0], toc.Mode, toc.Stereo, toc.FrameSize)
			}

			// Create stereo decoders
			channels := 2
			goDec, _ := gopus.NewDecoder(48000, channels)
			libDec, _ := NewLibopusDecoder(48000, channels)
			defer libDec.Destroy()

			// Analyze first 50 packets
			var sumSigPow, sumNoisePow float64
			worstSNR := float64(1000)
			var worstIdx int

			for i := 0; i < minInt(50, len(packets)); i++ {
				pkt := packets[i]
				if len(pkt) == 0 {
					continue
				}

				libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
				goPcm, _ := goDec.DecodeFloat32(pkt)

				if libSamples <= 0 || len(goPcm) == 0 {
					continue
				}

				var sigPow, noisePow float64
				for j := 0; j < minInt(len(goPcm), libSamples*channels); j++ {
					sig := float64(libPcm[j])
					noise := float64(goPcm[j]) - sig
					sigPow += sig * sig
					noisePow += noise * noise
				}

				sumSigPow += sigPow
				sumNoisePow += noisePow

				snr := 10 * math.Log10(sigPow/noisePow)
				if math.IsNaN(snr) || math.IsInf(snr, 1) {
					snr = 999
				}

				if snr < worstSNR {
					worstSNR = snr
					worstIdx = i
				}

				// Log packets with poor SNR
				if snr < 30 {
					toc := gopus.ParseTOC(pkt[0])
					t.Logf("Packet %d: SNR=%.1f dB mode=%d fs=%d len=%d",
						i, snr, toc.Mode, toc.FrameSize, len(pkt))
				}
			}

			overallSNR := 10 * math.Log10(sumSigPow/sumNoisePow)
			t.Logf("\nFirst 50 packets: overall SNR=%.1f dB, worst=%.1f dB at packet %d",
				overallSNR, worstSNR, worstIdx)

			// Examine the worst packet in detail
			if worstIdx < len(packets) {
				t.Logf("\nWorst packet %d details:", worstIdx)
				pkt := packets[worstIdx]
				toc := gopus.ParseTOC(pkt[0])

				// Fresh decoders for clean comparison
				goDec2, _ := gopus.NewDecoder(48000, channels)
				libDec2, _ := NewLibopusDecoder(48000, channels)
				defer libDec2.Destroy()

				// Sync state up to worst packet
				for i := 0; i < worstIdx; i++ {
					goDec2.DecodeFloat32(packets[i])
					libDec2.DecodeFloat(packets[i], 5760)
				}

				// Decode the worst packet
				libPcm, libSamples := libDec2.DecodeFloat(pkt, 5760)
				goPcm, _ := goDec2.DecodeFloat32(pkt)

				t.Logf("TOC=0x%02X mode=%d stereo=%v fs=%d len=%d",
					pkt[0], toc.Mode, toc.Stereo, toc.FrameSize, len(pkt))
				t.Logf("Samples: lib=%d go=%d", libSamples, len(goPcm)/channels)

				// Show first few samples (interleaved L/R)
				t.Logf("\nFirst 20 samples (L/R interleaved):")
				for j := 0; j < 20 && j < libSamples*channels; j++ {
					ch := "L"
					if j%2 == 1 {
						ch = "R"
					}
					t.Logf("  [%3d %s] lib=%.8f go=%.8f diff=%.2e",
						j/2, ch, libPcm[j], goPcm[j], float64(goPcm[j])-float64(libPcm[j]))
				}

				// Check if L and R channels have different error magnitudes
				var lSigPow, lNoisePow float64
				var rSigPow, rNoisePow float64
				for j := 0; j < libSamples*channels-1; j += 2 {
					// Left channel (even indices)
					lSig := float64(libPcm[j])
					lNoise := float64(goPcm[j]) - lSig
					lSigPow += lSig * lSig
					lNoisePow += lNoise * lNoise

					// Right channel (odd indices)
					rSig := float64(libPcm[j+1])
					rNoise := float64(goPcm[j+1]) - rSig
					rSigPow += rSig * rSig
					rNoisePow += rNoise * rNoise
				}

				lSNR := 10 * math.Log10(lSigPow/lNoisePow)
				rSNR := 10 * math.Log10(rSigPow/rNoisePow)

				t.Logf("\nChannel-specific SNR:")
				t.Logf("  Left channel:  %.1f dB", lSNR)
				t.Logf("  Right channel: %.1f dB", rSNR)
			}
		})
	}
}

// TestSilkMonoVsStereoComparison compares SILK mono (passing) vs stereo (failing)
func TestSilkMonoVsStereoComparison(t *testing.T) {
	t.Log("SILK mono tests (testvector02-04) pass with Q > 0")
	t.Log("SILK stereo tests (testvector08-09) fail with Q = -84.64")
	t.Log("\nKey difference: stereo prediction/interpolation in SILK")
	t.Log("Functions to investigate:")
	t.Log("  - silkStereoMSToLR (mid-side to left-right conversion)")
	t.Log("  - stereo prediction state management")
	t.Log("  - stereo index decoding")
}
