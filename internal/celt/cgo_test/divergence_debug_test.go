// divergence_debug_test.go - Debug where gopus/libopus state diverges
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestFindExactDivergencePoint examines packets 60-65 in detail
func TestFindExactDivergencePoint(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
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

	channels := 2

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Decode packets 0-65
	for i := 0; i <= 65 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, goErr := goDec.DecodeFloat32(pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		if goErr != nil || libSamples < 0 {
			t.Logf("Pkt %d: decode error (go=%v, lib=%d)", i, goErr, libSamples)
			continue
		}

		var sig, noise float64
		var maxDiff float64
		maxDiffIdx := 0
		n := minInt(len(goPcm), libSamples*channels)
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
			if math.Abs(d) > maxDiff {
				maxDiff = math.Abs(d)
				maxDiffIdx = j
			}
		}

		snr := 10 * math.Log10(sig/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		st := "mono"
		if toc.Stereo {
			st = "stereo"
		}

		// Show detailed info for packets 58-65
		if i >= 58 {
			t.Logf("\n=== Packet %d: frame=%d %s len=%d ===", i, toc.FrameSize, st, len(pkt))
			t.Logf("SNR: %.1f dB, maxDiff: %.8f at index %d", snr, maxDiff, maxDiffIdx)

			// Show first and last few samples
			t.Log("First 20 samples:")
			for j := 0; j < 20 && j < len(goPcm); j++ {
				diff := goPcm[j] - libPcm[j]
				ch := "L"
				if j%2 == 1 {
					ch = "R"
				}
				marker := ""
				if math.Abs(float64(diff)) > 0.0001 {
					marker = " *"
				}
				t.Logf("  [%3d %s] go=%12.8f lib=%12.8f diff=%12.8f%s", j/2, ch, goPcm[j], libPcm[j], diff, marker)
			}

			// Show samples around max diff
			if maxDiffIdx > 20 {
				t.Logf("Around max diff (idx=%d):", maxDiffIdx)
				start := maxDiffIdx - 4
				if start < 0 {
					start = 0
				}
				end := maxDiffIdx + 8
				if end > len(goPcm) {
					end = len(goPcm)
				}
				for j := start; j < end; j++ {
					diff := goPcm[j] - libPcm[j]
					ch := "L"
					if j%2 == 1 {
						ch = "R"
					}
					marker := ""
					if j == maxDiffIdx {
						marker = " <-- MAX"
					}
					t.Logf("  [%3d %s] go=%12.8f lib=%12.8f diff=%12.8f%s", j/2, ch, goPcm[j], libPcm[j], diff, marker)
				}
			}
		}
	}
}

// TestCheckDeemphasisState compares de-emphasis filter behavior
func TestCheckDeemphasisState(t *testing.T) {
	// Test de-emphasis filter with known input
	input := []float64{0.1, 0.2, 0.3, 0.2, 0.1, 0.0, -0.1, -0.2}

	// Expected behavior: y[n] = x[n] + 0.85 * y[n-1]
	// Starting from state = 0
	expected := make([]float64, len(input))
	state := 0.0
	for i := range input {
		tmp := input[i] + state
		state = 0.85 * tmp // PreemphCoef = 0.85
		expected[i] = tmp
	}

	t.Log("De-emphasis filter test:")
	t.Log("Input     Expected")
	for i := range input {
		t.Logf("%.6f  %.6f", input[i], expected[i])
	}
}
