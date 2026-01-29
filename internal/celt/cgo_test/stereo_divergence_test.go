// Package cgo investigates where SILK stereo divergence begins
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestStereoDivergencePoint traces exact divergence start
func TestStereoDivergencePoint(t *testing.T) {
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

	// Decode packets 0-20 and show detailed info for packets around divergence
	for pktIdx := 0; pktIdx < minInt(20, len(packets)); pktIdx++ {
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

		for _, v := range []*float64{&lSNR, &rSNR, &mSNR, &sSNR} {
			if math.IsNaN(*v) || math.IsInf(*v, 1) {
				*v = 999
			}
		}

		t.Logf("Pkt %2d: TOC=0x%02X mode=%d fs=%d len=%2d | L=%.1f R=%.1f M=%.1f S=%.1f",
			pktIdx, pkt[0], toc.Mode, toc.FrameSize, len(pkt),
			lSNR, rSNR, mSNR, sSNR)

		// Show detailed samples for packets 13-16
		if pktIdx >= 13 && pktIdx <= 16 {
			t.Logf("  First 5 samples:")
			for i := 0; i < minInt(5, numSamples); i++ {
				libL := libPcm[i*2]
				libR := libPcm[i*2+1]
				goL := goPcm[i*2]
				goR := goPcm[i*2+1]
				lDiff := float64(goL) - float64(libL)
				rDiff := float64(goR) - float64(libR)
				t.Logf("    [%d] L: lib=%.6f go=%.6f diff=%+.2e | R: lib=%.6f go=%.6f diff=%+.2e",
					i, libL, goL, lDiff, libR, goR, rDiff)
			}

			// Show raw packet bytes (first 10)
			bytesStr := ""
			for i := 0; i < minInt(10, len(pkt)); i++ {
				bytesStr += string(rune(pkt[i]>>4+'0')) + string(rune(pkt[i]&0xf+'0')) + " "
			}
			t.Logf("  Raw bytes: %02X", pkt[:minInt(10, len(pkt))])
		}
	}
}

// TestFreshDecodersAtPacket14 decodes packet 14 with fresh decoders
func TestFreshDecodersAtPacket14(t *testing.T) {
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

	// Test 1: Fresh decoders, decode only packet 14
	t.Logf("Test 1: Fresh decoders at packet 14")
	{
		goDec, _ := gopus.NewDecoder(48000, channels)
		libDec, _ := NewLibopusDecoder(48000, channels)
		defer libDec.Destroy()

		pkt := packets[14]
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := goDec.DecodeFloat32(pkt)

		var lSigPow, lNoisePow, rSigPow, rNoisePow float64
		for i := 0; i < libSamples; i++ {
			libL := float64(libPcm[i*2])
			libR := float64(libPcm[i*2+1])
			goL := float64(goPcm[i*2])
			goR := float64(goPcm[i*2+1])

			lSigPow += libL * libL
			lNoisePow += (goL - libL) * (goL - libL)
			rSigPow += libR * libR
			rNoisePow += (goR - libR) * (goR - libR)
		}

		lSNR := 10 * math.Log10(lSigPow/lNoisePow)
		rSNR := 10 * math.Log10(rSigPow/rNoisePow)
		t.Logf("  Fresh packet 14: L_SNR=%.1f R_SNR=%.1f", lSNR, rSNR)
	}

	// Test 2: Synced decoders up to packet 13, then decode 14
	t.Logf("\nTest 2: Synced decoders (0-13), then decode 14")
	{
		goDec, _ := gopus.NewDecoder(48000, channels)
		libDec, _ := NewLibopusDecoder(48000, channels)
		defer libDec.Destroy()

		for i := 0; i < 14; i++ {
			goDec.DecodeFloat32(packets[i])
			libDec.DecodeFloat(packets[i], 5760)
		}

		pkt := packets[14]
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		goPcm, _ := goDec.DecodeFloat32(pkt)

		var lSigPow, lNoisePow, rSigPow, rNoisePow float64
		for i := 0; i < libSamples; i++ {
			libL := float64(libPcm[i*2])
			libR := float64(libPcm[i*2+1])
			goL := float64(goPcm[i*2])
			goR := float64(goPcm[i*2+1])

			lSigPow += libL * libL
			lNoisePow += (goL - libL) * (goL - libL)
			rSigPow += libR * libR
			rNoisePow += (goR - libR) * (goR - libR)
		}

		lSNR := 10 * math.Log10(lSigPow/lNoisePow)
		rSNR := 10 * math.Log10(rSigPow/rNoisePow)
		t.Logf("  Synced packet 14: L_SNR=%.1f R_SNR=%.1f", lSNR, rSNR)

		// Show first few samples
		t.Logf("\n  First 5 samples of packet 14:")
		for i := 0; i < 5 && i < libSamples; i++ {
			libL := libPcm[i*2]
			libR := libPcm[i*2+1]
			goL := goPcm[i*2]
			goR := goPcm[i*2+1]
			t.Logf("    [%d] L: lib=%.6f go=%.6f | R: lib=%.6f go=%.6f",
				i, libL, goL, libR, goR)
		}
	}

	// Test 3: Check if packet 14 is different from 13
	t.Logf("\nTest 3: Compare packet 13 and 14 headers")
	{
		pkt13 := packets[13]
		pkt14 := packets[14]

		toc13 := gopus.ParseTOC(pkt13[0])
		toc14 := gopus.ParseTOC(pkt14[0])

		t.Logf("  Packet 13: TOC=0x%02X mode=%d fs=%d stereo=%v len=%d",
			pkt13[0], toc13.Mode, toc13.FrameSize, toc13.Stereo, len(pkt13))
		t.Logf("  Packet 14: TOC=0x%02X mode=%d fs=%d stereo=%v len=%d",
			pkt14[0], toc14.Mode, toc14.FrameSize, toc14.Stereo, len(pkt14))
		t.Logf("  Packet 13 bytes: %02X", pkt13[:minInt(20, len(pkt13))])
		t.Logf("  Packet 14 bytes: %02X", pkt14[:minInt(20, len(pkt14))])
	}
}
