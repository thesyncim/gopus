// Package cgo traces mid-only flag and side channel behavior
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestMidOnlyFlagPattern traces the decodeOnlyMiddle flag pattern
func TestMidOnlyFlagPattern(t *testing.T) {
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
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Trace packets around the divergence
	t.Logf("Tracing packet characteristics around divergence:")
	t.Logf("%3s  %4s  %3s  %4s  %10s  %10s  %10s  %10s",
		"pkt", "mode", "fs", "len", "lib_max_L", "lib_max_R", "go_max_L", "go_max_R")

	for pktIdx := 10; pktIdx < 20 && pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		if len(pkt) == 0 {
			continue
		}

		toc := gopus.ParseTOC(pkt[0])

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := goDec.DecodeFloat32(pkt)

		if libSamples <= 0 || len(goPcm) == 0 {
			continue
		}

		// Find max values for each channel
		var libMaxL, libMaxR, goMaxL, goMaxR float64
		for i := 0; i < libSamples; i++ {
			if l := math.Abs(float64(libPcm[i*2])); l > libMaxL {
				libMaxL = l
			}
			if r := math.Abs(float64(libPcm[i*2+1])); r > libMaxR {
				libMaxR = r
			}
			if l := math.Abs(float64(goPcm[i*2])); l > goMaxL {
				goMaxL = l
			}
			if r := math.Abs(float64(goPcm[i*2+1])); r > goMaxR {
				goMaxR = r
			}
		}

		t.Logf("%3d  %4d  %3d  %4d  %10.6f  %10.6f  %10.6f  %10.6f",
			pktIdx, toc.Mode, toc.FrameSize, len(pkt),
			libMaxL, libMaxR, goMaxL, goMaxR)
	}

	// Show detailed L/R comparison for packet 14
	t.Logf("\nDetailed analysis of packet 14:")
	// Reset decoders
	goDec2, _ := gopus.NewDecoder(48000, channels)
	libDec2, _ := NewLibopusDecoder(48000, channels)
	defer libDec2.Destroy()

	// Sync to packet 13
	for i := 0; i < 14; i++ {
		goDec2.DecodeFloat32(packets[i])
		libDec2.DecodeFloat(packets[i], 5760)
	}

	// Decode packet 14
	pkt := packets[14]
	libPcm, libSamples := libDec2.DecodeFloat(pkt, 5760)
	goPcm, _ := goDec2.DecodeFloat32(pkt)

	// Show side channel values (derived)
	t.Logf("\nSide channel (derived from L-R)/2 for first 10 samples:")
	t.Logf("%3s  %12s  %12s  %12s", "idx", "lib_S", "go_S", "diff_S")
	for i := 0; i < minInt(10, libSamples); i++ {
		libS := (float64(libPcm[i*2]) - float64(libPcm[i*2+1])) / 2
		goS := (float64(goPcm[i*2]) - float64(goPcm[i*2+1])) / 2
		t.Logf("%3d  %12.8f  %12.8f  %+.2e", i, libS, goS, goS-libS)
	}

	// Check if side signal is just wrong by a constant offset
	t.Logf("\nChecking for constant offset in side channel:")
	var sumDiffS float64
	for i := 0; i < libSamples; i++ {
		libS := (float64(libPcm[i*2]) - float64(libPcm[i*2+1])) / 2
		goS := (float64(goPcm[i*2]) - float64(goPcm[i*2+1])) / 2
		sumDiffS += goS - libS
	}
	avgDiffS := sumDiffS / float64(libSamples)
	t.Logf("Average diff_S: %.6f", avgDiffS)

	// Check if there's a scaling factor
	var sumLibS2, sumGoS2 float64
	for i := 0; i < libSamples; i++ {
		libS := (float64(libPcm[i*2]) - float64(libPcm[i*2+1])) / 2
		goS := (float64(goPcm[i*2]) - float64(goPcm[i*2+1])) / 2
		sumLibS2 += libS * libS
		sumGoS2 += goS * goS
	}
	if sumLibS2 > 0 {
		scaleRatio := math.Sqrt(sumGoS2 / sumLibS2)
		t.Logf("Scale ratio (go_S/lib_S by energy): %.6f", scaleRatio)
	}
}
