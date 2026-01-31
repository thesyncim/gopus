// imdct_short_compare_test.go - Compare short block IMDCT between gopus and libopus
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// TestShortBlockIMDCTValues compares the actual IMDCT computation for short blocks
func TestShortBlockIMDCTValues(t *testing.T) {
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

	// Build up decoder state
	goDec, _ := gopus.NewDecoderDefault(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	for i := 0; i < 61; i++ {
		decodeFloat32(goDec, packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode packet 61 (transient)
	pkt61 := packets[61]
	toc := gopus.ParseTOC(pkt61[0])

	t.Logf("Packet 61: frameSize=%d, mode=%d", toc.FrameSize, toc.Mode)

	// Decode with both
	goPcm, _ := decodeFloat32(goDec, pkt61)
	libPcm, libN := libDec.DecodeFloat(pkt61, 5760)

	// Compare first few output samples after overlap region
	t.Log("\nComparing output samples around overlap boundaries:")
	t.Log("Position | Go Output | Lib Output | Difference")
	t.Log("---------+-----------+------------+-----------")

	channels := 2
	samplePositions := []int{0, 60, 119, 120, 121, 180, 239, 240, 360, 480, 600, 720, 840, 959}
	for _, pos := range samplePositions {
		for ch := 0; ch < channels; ch++ {
			idx := pos*channels + ch
			if idx < len(goPcm) && idx < libN*channels {
				goVal := goPcm[idx]
				libVal := libPcm[idx]
				diff := math.Abs(float64(goVal) - float64(libVal))
				if diff > 0.001 || pos < 5 || pos == 120 || pos == 240 {
					t.Logf(" %4d.%d |%10.6f |%10.6f | %.6e", pos, ch, goVal, libVal, diff)
				}
			}
		}
	}

	// Compute per-sample differences for the first 240 samples
	t.Log("\nMax diff per 60-sample segment (overlap/2 boundaries):")
	for seg := 0; seg < 16; seg++ {
		startSample := seg * 60
		endSample := (seg + 1) * 60
		if endSample > libN {
			break
		}

		var maxDiff float64
		var maxDiffPos int
		for i := startSample; i < endSample; i++ {
			for ch := 0; ch < channels; ch++ {
				idx := i*channels + ch
				if idx < len(goPcm) && idx < libN*channels {
					diff := math.Abs(float64(goPcm[idx]) - float64(libPcm[idx]))
					if diff > maxDiff {
						maxDiff = diff
						maxDiffPos = i
					}
				}
			}
		}
		t.Logf("  Segment %2d [%4d-%4d]: max_diff=%.6e at sample %d", seg, startSample, endSample-1, maxDiff, maxDiffPos)
	}
}

// TestOverlapRegionDetailed examines the exact overlap region values
func TestOverlapRegionDetailed(t *testing.T) {
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

	// Create fresh decoders
	goDec, _ := gopus.NewDecoderDefault(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	// Decode up to packet 65 to see around transient
	for i := 0; i < 66 && i < len(packets); i++ {
		goPcm, _ := decodeFloat32(goDec, packets[i])
		libPcm, libN := libDec.DecodeFloat(packets[i], 5760)

		// Show overlap buffer energy after each frame
		goOverlap := goDec.GetCELTDecoder().OverlapBuffer()
		var goEnergy float64
		for _, v := range goOverlap {
			goEnergy += v * v
		}

		// Compute frame SNR
		n := minInt(len(goPcm), libN*2)
		var sig, noise float64
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
		}
		snr := 10 * math.Log10(sig/noise)

		if i >= 58 {
			toc := gopus.ParseTOC(packets[i][0])
			transient := ""
			// Check if this is a transient frame (parse the CELT header)
			if toc.Mode == gopus.ModeCELT && len(packets[i]) > 1 {
				// Simple heuristic: packet 61 is known transient
				if i == 61 {
					transient = " (TRANSIENT)"
				}
			}
			t.Logf("Frame %2d: mode=%d frameSize=%d overlapEnergy=%.6f SNR=%.1f dB%s",
				i, toc.Mode, toc.FrameSize, goEnergy, snr, transient)
		}
	}
}

// TestCompareDeemphasisInput compares the input to deemphasis (after IMDCT+overlap)
func TestCompareDeemphasisInput(t *testing.T) {
	// This test tries to isolate whether the difference is before or after deemphasis
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

	// Test with just VorbisWindow to ensure it matches libopus
	window := celt.GetWindowBuffer(120)
	libWindow := []float64{
		6.7286966e-05, 0.00060551348, 0.0016815970, 0.0032947962, 0.0054439943,
		0.0081276923, 0.011344001, 0.015090633, 0.019364886, 0.024163635,
	}

	t.Log("Window comparison (first 10 values):")
	for i := 0; i < 10; i++ {
		diff := math.Abs(window[i] - libWindow[i])
		t.Logf("  [%d] go=%.8e lib=%.8e diff=%.8e", i, window[i], libWindow[i], diff)
	}
}
