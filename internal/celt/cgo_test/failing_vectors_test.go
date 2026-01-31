// failing_vectors_test.go - Analyze failing compliance test vectors
package cgo

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

func loadVectorPackets(t *testing.T, vectorName string) [][]byte {
	t.Helper()
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + vectorName + ".bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return nil
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4 // skip enc_final_range

		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}

		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}
	return packets
}

func getVectorChannels(vectorName string) int {
	// Based on test vectors documentation:
	// 01: stereo CELT
	// 02-04: mono SILK
	// 05-06: mono Hybrid
	// 07: stereo CELT
	// 08-09: stereo mixed SILK/CELT
	// 10: stereo mixed CELT/Hybrid
	// 11: stereo CELT
	// 12: mono mixed Hybrid/SILK
	switch vectorName {
	case "testvector02", "testvector03", "testvector04", "testvector05", "testvector06", "testvector12":
		return 1
	default:
		return 2
	}
}

// TestAnalyzeFailingVector08 analyzes testvector08 (mixed SILK+CELT)
func TestAnalyzeFailingVector08(t *testing.T) {
	analyzeVector(t, "testvector08")
}

// TestAnalyzeFailingVector09 analyzes testvector09 (mixed SILK+CELT)
func TestAnalyzeFailingVector09(t *testing.T) {
	analyzeVector(t, "testvector09")
}

// TestAnalyzeFailingVector10 analyzes testvector10 (mixed CELT+Hybrid)
func TestAnalyzeFailingVector10(t *testing.T) {
	analyzeVector(t, "testvector10")
}

// TestAnalyzeFailingVector12 analyzes testvector12 (mixed Hybrid+SILK)
func TestAnalyzeFailingVector12(t *testing.T) {
	analyzeVector(t, "testvector12")
}

// TestAnalyzeFailingVector06 analyzes testvector06 (Hybrid)
func TestAnalyzeFailingVector06(t *testing.T) {
	analyzeVector(t, "testvector06")
}

// TestAnalyzeFailingVector07 analyzes testvector07 (CELT various frame sizes)
func TestAnalyzeFailingVector07(t *testing.T) {
	analyzeVector(t, "testvector07")
}

func analyzeVector(t *testing.T, vectorName string) {
	packets := loadVectorPackets(t, vectorName)
	if len(packets) == 0 {
		t.Skipf("No packets in %s", vectorName)
		return
	}

	channels := getVectorChannels(vectorName)
	t.Logf("Analyzing %s: %d packets, %d channels", vectorName, len(packets), channels)

	// Create decoders
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	var prevMode gopus.Mode
	var prevToc gopus.TOC
	modeTransitionCount := 0

	type packetStats struct {
		idx          int
		snr          float64
		mode         gopus.Mode
		frameSize    int
		stereo       bool
		isTransition bool
		prevMode     gopus.Mode
	}
	var badPackets []packetStats
	var totalSig, totalNoise float64

	for i, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		isTransition := (i > 0) && (toc.Mode != prevMode)
		if isTransition {
			modeTransitionCount++
		}

		goPcm, decErr := decodeFloat32(goDec, pkt)
		if decErr != nil {
			t.Logf("Packet %d: gopus error: %v", i, decErr)
			prevMode = toc.Mode
			prevToc = toc
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples < 0 {
			t.Logf("Packet %d: libopus error: %d", i, libSamples)
			prevMode = toc.Mode
			prevToc = toc
			continue
		}

		goSamples := len(goPcm) / channels
		if goSamples != libSamples {
			t.Logf("Packet %d: sample count mismatch go=%d lib=%d", i, goSamples, libSamples)
			prevMode = toc.Mode
			prevToc = toc
			continue
		}

		var sig, noise float64
		for j := 0; j < goSamples*channels; j++ {
			s := float64(libPcm[j])
			n := float64(goPcm[j]) - s
			sig += s * s
			noise += n * n
		}

		totalSig += sig
		totalNoise += noise

		snr := 10 * math.Log10(sig/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		if snr < 40 {
			badPackets = append(badPackets, packetStats{
				idx:          i,
				snr:          snr,
				mode:         toc.Mode,
				frameSize:    toc.FrameSize,
				stereo:       toc.Stereo,
				isTransition: isTransition,
				prevMode:     prevMode,
			})
		}

		// Log first bad packet in detail
		if len(badPackets) == 1 {
			t.Logf("\n*** First bad packet at %d ***", i)
			t.Logf("  Mode: %v (previous: %v)", toc.Mode, prevMode)
			t.Logf("  Frame size: %d, Stereo: %v", toc.FrameSize, toc.Stereo)
			t.Logf("  SNR: %.2f dB", snr)
			t.Logf("  Is mode transition: %v", isTransition)
			if isTransition {
				t.Logf("  Previous packet: mode=%v, frame=%d", prevToc.Mode, prevToc.FrameSize)
			}

			// Show first few samples
			maxShow := 20
			if goSamples*channels < maxShow {
				maxShow = goSamples * channels
			}
			t.Logf("  First %d samples:", maxShow)
			for k := 0; k < maxShow; k++ {
				diff := goPcm[k] - libPcm[k]
				marker := ""
				if math.Abs(float64(diff)) > 0.001 {
					marker = " *"
				}
				t.Logf("    [%d] go=%.6f lib=%.6f diff=%.6f%s", k, goPcm[k], libPcm[k], diff, marker)
			}
		}

		prevMode = toc.Mode
		prevToc = toc
	}

	overallSNR := 10 * math.Log10(totalSig/totalNoise)
	t.Logf("\n=== Summary for %s ===", vectorName)
	t.Logf("Overall SNR: %.2f dB", overallSNR)
	t.Logf("Mode transitions: %d", modeTransitionCount)
	t.Logf("Bad packets (SNR<40dB): %d / %d (%.1f%%)", len(badPackets), len(packets), 100*float64(len(badPackets))/float64(len(packets)))

	// Categorize bad packets
	transitionBad := 0
	nonTransitionBad := 0
	for _, bp := range badPackets {
		if bp.isTransition {
			transitionBad++
		} else {
			nonTransitionBad++
		}
	}
	t.Logf("Bad packets at mode transitions: %d", transitionBad)
	t.Logf("Bad packets not at transitions: %d", nonTransitionBad)

	// Group by mode
	modeCount := make(map[gopus.Mode]int)
	modeBad := make(map[gopus.Mode]int)
	for _, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		modeCount[toc.Mode]++
	}
	for _, bp := range badPackets {
		modeBad[bp.mode]++
	}

	t.Logf("\nBy mode:")
	for mode, count := range modeCount {
		bad := modeBad[mode]
		pct := 0.0
		if count > 0 {
			pct = 100 * float64(bad) / float64(count)
		}
		t.Logf("  %v: %d bad / %d total (%.1f%%)", mode, bad, count, pct)
	}

	// Show first 10 bad packets
	if len(badPackets) > 0 {
		t.Logf("\nFirst 10 bad packets:")
		for i := 0; i < len(badPackets) && i < 10; i++ {
			bp := badPackets[i]
			transStr := ""
			if bp.isTransition {
				transStr = fmt.Sprintf(" [MODE TRANSITION from %v]", bp.prevMode)
			}
			t.Logf("  Pkt %d: SNR=%.1f dB, mode=%v, frame=%d, stereo=%v%s",
				bp.idx, bp.snr, bp.mode, bp.frameSize, bp.stereo, transStr)
		}
	}
}

// TestFindModeTransitions lists all mode transitions in a vector
func TestFindModeTransitions(t *testing.T) {
	vectors := []string{"testvector08", "testvector09", "testvector10", "testvector12"}

	for _, vec := range vectors {
		packets := loadVectorPackets(t, vec)
		if len(packets) == 0 {
			continue
		}

		t.Logf("\n=== Mode transitions in %s ===", vec)

		var prevMode gopus.Mode
		for i, pkt := range packets {
			toc := gopus.ParseTOC(pkt[0])
			if i > 0 && toc.Mode != prevMode {
				t.Logf("  Pkt %d: %v -> %v (frame=%d, stereo=%v)",
					i, prevMode, toc.Mode, toc.FrameSize, toc.Stereo)
			}
			prevMode = toc.Mode
		}
	}
}

// TestCompareAroundTransition compares decoding around a specific mode transition
func TestCompareAroundTransition(t *testing.T) {
	// testvector08 has SILK->CELT transitions
	packets := loadVectorPackets(t, "testvector08")
	if len(packets) == 0 {
		t.Skip("No packets")
		return
	}

	channels := 2

	// Find first SILK->CELT transition
	var transitionIdx int
	var prevMode gopus.Mode
	for i, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		if i > 0 && prevMode == gopus.ModeSILK && toc.Mode == gopus.ModeCELT {
			transitionIdx = i
			t.Logf("Found SILK->CELT transition at packet %d", i)
			break
		}
		prevMode = toc.Mode
	}

	if transitionIdx == 0 {
		t.Skip("No SILK->CELT transition found")
		return
	}

	// Decode with fresh decoders from the beginning
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Decode all packets up to and including the transition
	start := maxInt(0, transitionIdx-3)
	for i := 0; i < transitionIdx+5 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, goErr := decodeFloat32(goDec, pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		if i >= start {
			marker := ""
			if i == transitionIdx {
				marker = " <-- TRANSITION"
			}

			var snr float64
			if goErr == nil && libSamples > 0 {
				var sig, noise float64
				n := minInt(len(goPcm), libSamples*channels)
				for j := 0; j < n; j++ {
					s := float64(libPcm[j])
					d := float64(goPcm[j]) - s
					sig += s * s
					noise += d * d
				}
				snr = 10 * math.Log10(sig/noise)
				if math.IsNaN(snr) || math.IsInf(snr, 1) {
					snr = 999
				}
			}

			t.Logf("Pkt %d: mode=%v frame=%d stereo=%v SNR=%.1f dB%s",
				i, toc.Mode, toc.FrameSize, toc.Stereo, snr, marker)
		}
	}
}
