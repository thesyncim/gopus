// Package cgo traces stereo prediction values
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTracePredictorValues traces predQ13 values for each packet
func TestTracePredictorValues(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector08.bit"
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

	// We need to decode with gopus and extract predQ13 values
	// Since predQ13 is internal, we'll compare outputs with libopus using known good/bad patterns

	t.Logf("Analyzing packet structure around divergence point:")
	for pktIdx := 12; pktIdx < 17 && pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		if len(pkt) == 0 {
			continue
		}

		toc := gopus.ParseTOC(pkt[0])

		// Show packet structure
		t.Logf("\nPacket %d: TOC=0x%02X mode=%d fs=%d len=%d",
			pktIdx, pkt[0], toc.Mode, toc.FrameSize, len(pkt))

		// For Hybrid mode (mode=2), the structure is:
		// - TOC byte
		// - Stereo predictor (if stereo)
		// - SILK data
		// - CELT data

		// Stereo predictor is read first by silkStereoDecodePred
		// The first few bits after TOC encode the predictor indices

		// Show first few bytes
		t.Logf("  Raw payload (after TOC): % 02X", pkt[1:minInt(20, len(pkt))])

		// Parse the stereo predictor manually
		// The range coder reads:
		// 1. Joint index (using silk_stereo_pred_joint_iCDF)
		// 2. Two sets of (fine_index, step_index)

		// Without access to range decoder internals, let's just show the pattern

		// Actually, let me check if there's something specific about packet 14
		if pktIdx == 13 || pktIdx == 14 {
			t.Logf("  Binary of first 4 payload bytes:")
			for i := 1; i < minInt(5, len(pkt)); i++ {
				t.Logf("    Byte %d: 0x%02X = %08b", i, pkt[i], pkt[i])
			}
		}
	}
}

// TestCompareLibopusInternals compares outputs in more detail
func TestCompareLibopusInternals(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector08.bit"
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

	// Decode packet 14 with fresh decoders and compare in detail
	t.Logf("Detailed comparison of packet 14 with fresh decoders:")

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	pkt := packets[14]
	toc := gopus.ParseTOC(pkt[0])

	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	goPcm, _ := goDec.DecodeFloat32(pkt)

	t.Logf("Packet 14: TOC=0x%02X mode=%d fs=%d", pkt[0], toc.Mode, toc.FrameSize)
	t.Logf("  Samples: lib=%d go=%d", libSamples, len(goPcm)/channels)

	// Show error progression through the frame
	t.Logf("\n  Error progression (every 10th sample):")
	t.Logf("  %5s  %12s  %12s  %12s  %12s  %12s  %12s",
		"idx", "lib_L", "go_L", "diff_L", "lib_R", "go_R", "diff_R")

	for i := 0; i < libSamples && i*channels+1 < len(goPcm); i += 10 {
		libL := libPcm[i*2]
		libR := libPcm[i*2+1]
		goL := goPcm[i*2]
		goR := goPcm[i*2+1]
		lDiff := float64(goL) - float64(libL)
		rDiff := float64(goR) - float64(libR)

		t.Logf("  %5d  %12.8f  %12.8f  %+.2e  %12.8f  %12.8f  %+.2e",
			i, libL, goL, lDiff, libR, goR, rDiff)
	}

	// Compute derived mid/side to check where the error is
	t.Logf("\n  Derived mid/side error (every 10th sample):")
	t.Logf("  %5s  %12s  %12s  %12s  %12s  %12s  %12s",
		"idx", "lib_M", "go_M", "diff_M", "lib_S", "go_S", "diff_S")

	for i := 0; i < libSamples && i*channels+1 < len(goPcm); i += 10 {
		libL := float64(libPcm[i*2])
		libR := float64(libPcm[i*2+1])
		goL := float64(goPcm[i*2])
		goR := float64(goPcm[i*2+1])

		// M = (L+R)/2, S = (L-R)/2
		libM := (libL + libR) / 2
		libS := (libL - libR) / 2
		goM := (goL + goR) / 2
		goS := (goL - goR) / 2

		mDiff := goM - libM
		sDiff := goS - libS

		t.Logf("  %5d  %12.8f  %12.8f  %+.2e  %12.8f  %12.8f  %+.2e",
			i, libM, goM, mDiff, libS, goS, sDiff)
	}
}
