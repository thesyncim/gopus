//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestVectorModeInfo shows what modes/bandwidths each test vector uses.
func TestVectorModeInfo(t *testing.T) {
	vectors := []string{
		"testvector01", "testvector02", "testvector03", "testvector04",
		"testvector05", "testvector06", "testvector07", "testvector08",
		"testvector09", "testvector10", "testvector11", "testvector12",
	}

	modeNames := []string{"SILK", "Hybrid", "CELT"}
	bwNames := []string{"NB", "MB", "WB", "SWB", "FB"}

	for _, v := range vectors {
		bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + v + ".bit"

		packets, err := loadPacketsSimple(bitFile, 10)
		if err != nil {
			t.Logf("%s: could not load", v)
			continue
		}

		// Collect unique configs
		configs := make(map[string]int)
		for _, pkt := range packets {
			toc := gopus.ParseTOC(pkt[0])
			modeStr := modeNames[toc.Mode]
			bwStr := "?"
			if int(toc.Bandwidth) < len(bwNames) {
				bwStr = bwNames[toc.Bandwidth]
			}
			stereoStr := "mono"
			if toc.Stereo {
				stereoStr = "stereo"
			}
			key := modeStr + " " + bwStr + " " + stereoStr
			configs[key]++
		}

		configStrs := []string{}
		for k := range configs {
			configStrs = append(configStrs, k)
		}

		t.Logf("%s: %v", v, configStrs)
	}
}

// TestSILK03Details shows detailed info for testvector03.
func TestSILK03Details(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector03.bit"

	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil {
		t.Skip("Could not load packets")
	}

	modeNames := []string{"SILK", "Hybrid", "CELT"}
	bwNames := []string{"NB", "MB", "WB", "SWB", "FB"}

	for i, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		modeStr := modeNames[toc.Mode]
		bwStr := "?"
		if int(toc.Bandwidth) < len(bwNames) {
			bwStr = bwNames[toc.Bandwidth]
		}
		stereoStr := "mono"
		if toc.Stereo {
			stereoStr = "stereo"
		}

		t.Logf("Packet %d: %s %s %s, frameSize=%d", i, modeStr, bwStr, stereoStr, toc.FrameSize)
	}
}
