// Package cgo traces SILK stereo MS-to-LR conversion in detail
package cgo

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestSilkStereoDetailedTrace compares L/R channels sample by sample
func TestSilkStereoDetailedTrace(t *testing.T) {
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

	// Analyze first few packets in detail
	for pktIdx := 0; pktIdx < minInt(5, len(packets)); pktIdx++ {
		pkt := packets[pktIdx]
		if len(pkt) == 0 {
			continue
		}

		toc := gopus.ParseTOC(pkt[0])
		t.Logf("\n=== Packet %d ===", pktIdx)
		t.Logf("TOC: 0x%02X mode=%d stereo=%v fs=%d len=%d",
			pkt[0], toc.Mode, toc.Stereo, toc.FrameSize, len(pkt))

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := goDec.DecodeFloat32(pkt)

		if libSamples <= 0 || len(goPcm) == 0 {
			t.Logf("Decode failed")
			continue
		}

		// Split into L/R channels
		numSamples := libSamples
		libL := make([]float32, numSamples)
		libR := make([]float32, numSamples)
		goL := make([]float32, numSamples)
		goR := make([]float32, numSamples)

		for i := 0; i < numSamples; i++ {
			libL[i] = libPcm[i*2]
			libR[i] = libPcm[i*2+1]
			if i*2+1 < len(goPcm) {
				goL[i] = goPcm[i*2]
				goR[i] = goPcm[i*2+1]
			}
		}

		// Calculate SNR for each channel
		var lSigPow, lNoisePow float64
		var rSigPow, rNoisePow float64
		var lMaxDiff, rMaxDiff float64
		var lMaxDiffIdx, rMaxDiffIdx int

		for i := 0; i < numSamples; i++ {
			lSig := float64(libL[i])
			lNoise := float64(goL[i]) - lSig
			lSigPow += lSig * lSig
			lNoisePow += lNoise * lNoise
			if d := math.Abs(lNoise); d > lMaxDiff {
				lMaxDiff = d
				lMaxDiffIdx = i
			}

			rSig := float64(libR[i])
			rNoise := float64(goR[i]) - rSig
			rSigPow += rSig * rSig
			rNoisePow += rNoise * rNoise
			if d := math.Abs(rNoise); d > rMaxDiff {
				rMaxDiff = d
				rMaxDiffIdx = i
			}
		}

		lSNR := 10 * math.Log10(lSigPow/lNoisePow)
		rSNR := 10 * math.Log10(rSigPow/rNoisePow)

		t.Logf("Left channel:  SNR=%.1f dB, maxDiff=%.2e at sample %d", lSNR, lMaxDiff, lMaxDiffIdx)
		t.Logf("Right channel: SNR=%.1f dB, maxDiff=%.2e at sample %d", rSNR, rMaxDiff, rMaxDiffIdx)

		// Show first 10 samples of each channel
		t.Logf("\nFirst 10 samples:")
		t.Logf("%5s  %12s  %12s  %12s | %12s  %12s  %12s",
			"idx", "lib_L", "go_L", "diff_L", "lib_R", "go_R", "diff_R")
		for i := 0; i < minInt(10, numSamples); i++ {
			lDiff := float64(goL[i]) - float64(libL[i])
			rDiff := float64(goR[i]) - float64(libR[i])
			t.Logf("%5d  %12.8f  %12.8f  %+.2e | %12.8f  %12.8f  %+.2e",
				i, libL[i], goL[i], lDiff, libR[i], goR[i], rDiff)
		}

		// Check mid/side relationship
		// L = M + S, R = M - S
		// So: M = (L+R)/2, S = (L-R)/2
		t.Logf("\nDerived mid/side from output:")
		t.Logf("%5s  %12s  %12s  %12s | %12s  %12s  %12s",
			"idx", "lib_M", "go_M", "diff_M", "lib_S", "go_S", "diff_S")
		for i := 0; i < minInt(10, numSamples); i++ {
			libM := (libL[i] + libR[i]) / 2
			libS := (libL[i] - libR[i]) / 2
			goM := (goL[i] + goR[i]) / 2
			goS := (goL[i] - goR[i]) / 2
			mDiff := float64(goM) - float64(libM)
			sDiff := float64(goS) - float64(libS)
			t.Logf("%5d  %12.8f  %12.8f  %+.2e | %12.8f  %12.8f  %+.2e",
				i, libM, goM, mDiff, libS, goS, sDiff)
		}
	}
}

// TestCompareRawMidSide compares mid and side signals before MS-to-LR
func TestCompareRawMidSide(t *testing.T) {
	// This test would require internal access to gopus SILK decoder
	// For now, analyze the pattern of errors

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

	// Analyze error pattern across many packets
	t.Logf("Analyzing error pattern across packets:")
	t.Logf("%5s  %10s  %10s  %10s  %10s", "pkt", "L_SNR", "R_SNR", "M_SNR", "S_SNR")

	for pktIdx := 0; pktIdx < minInt(50, len(packets)); pktIdx++ {
		pkt := packets[pktIdx]
		if len(pkt) == 0 {
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := goDec.DecodeFloat32(pkt)

		if libSamples <= 0 || len(goPcm) == 0 {
			continue
		}

		numSamples := libSamples
		var lSigPow, lNoisePow float64
		var rSigPow, rNoisePow float64
		var mSigPow, mNoisePow float64
		var sSigPow, sNoisePow float64

		for i := 0; i < numSamples; i++ {
			libL := float64(libPcm[i*2])
			libR := float64(libPcm[i*2+1])
			goL := float64(goPcm[i*2])
			goR := float64(goPcm[i*2+1])

			lSigPow += libL * libL
			lNoisePow += (goL - libL) * (goL - libL)

			rSigPow += libR * libR
			rNoisePow += (goR - libR) * (goR - libR)

			// Mid = (L+R)/2, Side = (L-R)/2
			libM := (libL + libR) / 2
			libS := (libL - libR) / 2
			goM := (goL + goR) / 2
			goS := (goL - goR) / 2

			mSigPow += libM * libM
			mNoisePow += (goM - libM) * (goM - libM)

			sSigPow += libS * libS
			sNoisePow += (goS - libS) * (goS - libS)
		}

		lSNR := 10 * math.Log10(lSigPow/lNoisePow)
		rSNR := 10 * math.Log10(rSigPow/rNoisePow)
		mSNR := 10 * math.Log10(mSigPow/mNoisePow)
		sSNR := 10 * math.Log10(sSigPow/sNoisePow)

		if math.IsNaN(lSNR) {
			lSNR = 999
		}
		if math.IsNaN(rSNR) {
			rSNR = 999
		}
		if math.IsNaN(mSNR) {
			mSNR = 999
		}
		if math.IsNaN(sSNR) {
			sSNR = 999
		}

		t.Logf("%5d  %10.1f  %10.1f  %10.1f  %10.1f", pktIdx, lSNR, rSNR, mSNR, sSNR)
	}
}

func formatFloat(f float32) string {
	return fmt.Sprintf("%.8f", f)
}
