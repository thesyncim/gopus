//go:build cgo_libopus
// +build cgo_libopus

// overlap_buffer_compare_test.go - Compare overlap buffer values between gopus and libopus
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestOverlapBufferAfterTransient compares overlap buffers after each frame
func TestOverlapBufferAfterTransient(t *testing.T) {
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

	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	// Decode frames around the transient
	for i := 0; i < 65 && i < len(packets); i++ {
		decodeFloat32(goDec, packets[i])
		libDec.DecodeFloat(packets[i], 5760)

		if i >= 59 && i <= 63 {
			// Get overlap buffers
			goOverlap := goDec.GetCELTDecoder().OverlapBuffer()

			// Get libopus preemph state (closest thing we can access)
			libMem0, libMem1 := libDec.GetPreemphState()

			// Get gopus preemph state
			goState := goDec.GetCELTDecoder().PreemphState()

			t.Logf("\n=== After Frame %d ===", i)
			t.Logf("  Go overlap length: %d", len(goOverlap))
			t.Logf("  Libopus preemph state: [%.8f, %.8f]", libMem0, libMem1)
			t.Logf("  Gopus preemph state: [%.8f, %.8f]", goState[0], goState[1])
			t.Logf("  Preemph state error: [%.8e, %.8e]",
				math.Abs(goState[0]-float64(libMem0)),
				math.Abs(goState[1]-float64(libMem1)))

			// Show first/last few overlap values
			if len(goOverlap) >= 10 {
				t.Logf("  Go overlap[0:5]: [%.6f, %.6f, %.6f, %.6f, %.6f]",
					goOverlap[0], goOverlap[1], goOverlap[2], goOverlap[3], goOverlap[4])
				t.Logf("  Go overlap[%d:%d]: [%.6f, %.6f, %.6f, %.6f, %.6f]",
					len(goOverlap)-5, len(goOverlap),
					goOverlap[len(goOverlap)-5], goOverlap[len(goOverlap)-4],
					goOverlap[len(goOverlap)-3], goOverlap[len(goOverlap)-2], goOverlap[len(goOverlap)-1])
			}

			// Compute overlap buffer energy
			var goEnergy float64
			for _, v := range goOverlap {
				goEnergy += v * v
			}
			t.Logf("  Go overlap energy: %.6f", goEnergy)
		}
	}
}

// TestDetailedFrameComparison does detailed sample comparison for frames around transient
func TestDetailedFrameComparison(t *testing.T) {
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

	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	// Decode frames
	for i := 0; i < 65 && i < len(packets); i++ {
		goPcm, _ := decodeFloat32(goDec, packets[i])
		libPcm, libN := libDec.DecodeFloat(packets[i], 5760)

		if i >= 60 && i <= 62 {
			t.Logf("\n=== Frame %d ===", i)
			toc := gopus.ParseTOC(packets[i][0])
			t.Logf("  frameSize=%d, samples=%d", toc.FrameSize, libN)

			// Compare first and last samples
			t.Logf("  First 5 samples (per channel):")
			for j := 0; j < 5 && j < libN; j++ {
				goL := goPcm[j*2]
				goR := goPcm[j*2+1]
				libL := libPcm[j*2]
				libR := libPcm[j*2+1]
				t.Logf("    [%d] L: go=%.6f lib=%.6f diff=%.6e | R: go=%.6f lib=%.6f diff=%.6e",
					j, goL, libL, float64(goL)-float64(libL), goR, libR, float64(goR)-float64(libR))
			}

			t.Logf("  Last 5 samples:")
			for j := libN - 5; j < libN; j++ {
				if j >= 0 {
					goL := goPcm[j*2]
					goR := goPcm[j*2+1]
					libL := libPcm[j*2]
					libR := libPcm[j*2+1]
					t.Logf("    [%d] L: go=%.6f lib=%.6f diff=%.6e | R: go=%.6f lib=%.6f diff=%.6e",
						j, goL, libL, float64(goL)-float64(libL), goR, libR, float64(goR)-float64(libR))
				}
			}

			// Find max diff
			var maxDiff float64
			maxDiffPos := 0
			for j := 0; j < libN*2 && j < len(goPcm); j++ {
				diff := math.Abs(float64(goPcm[j]) - float64(libPcm[j]))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffPos = j / 2
				}
			}
			t.Logf("  Max diff: %.6e at sample %d", maxDiff, maxDiffPos)

			// Compute SNR
			n := minInt(len(goPcm), libN*2)
			var sig, noise float64
			for j := 0; j < n; j++ {
				s := float64(libPcm[j])
				d := float64(goPcm[j]) - s
				sig += s * s
				noise += d * d
			}
			snr := 10 * math.Log10(sig/noise)
			t.Logf("  SNR: %.1f dB", snr)
		}
	}
}
