//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests the sMid hypothesis for TV12 divergence.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestTV12Packet826SMidEffect tests if bypassing sMid fixes the divergence.
func TestTV12Packet826SMidEffect(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create libopus 48kHz decoder for reference
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process packets 0-826 with libopus to get reference output for packet 826
	var libOut826 []float32
	var libSamples826 int
	for i := 0; i <= 826; i++ {
		pkt := packets[i]
		out, n := libDec.DecodeFloat(pkt, 1920)
		if i == 826 {
			libOut826 = out
			libSamples826 = n
		}
	}

	// --- Method 1: Current approach (with sMid shifting) ---
	silkDec1 := silk.NewDecoder()
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		silkDec1.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	}

	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	// Use the full Decode which includes sMid shifting
	output1, _ := silkDec1.Decode(pkt[1:], silkBW, toc.FrameSize, true)

	// --- Method 2: Direct pass without sMid shifting ---
	silkDec2 := silk.NewDecoder()
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		silkDec2.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	}

	// Handle bandwidth change for resampler
	silkDec2.HandleBandwidthChange(silkBW)

	// Decode at native rate
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	nativeSamples, err := silkDec2.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}

	// Resample directly without sMid shifting
	resampler := silkDec2.GetResampler(silkBW)
	output2 := resampler.Process(nativeSamples)

	t.Logf("Packet 826: Mode=%v, BW=%v", toc.Mode, toc.Bandwidth)
	t.Logf("Native samples: %d", len(nativeSamples))
	t.Logf("Output1 (with sMid): %d samples", len(output1))
	t.Logf("Output2 (direct): %d samples", len(output2))
	t.Logf("LibOpus: %d samples", libSamples826)

	// Calculate SNR for both methods
	minLen := len(output1)
	if libSamples826 < minLen {
		minLen = libSamples826
	}
	if len(output2) < minLen {
		minLen = len(output2)
	}

	var sumSqErr1, sumSqErr2, sumSqSig float64
	for i := 0; i < minLen; i++ {
		libVal := float64(libOut826[i])
		sumSqSig += libVal * libVal

		diff1 := float64(output1[i]) - libVal
		sumSqErr1 += diff1 * diff1

		diff2 := float64(output2[i]) - libVal
		sumSqErr2 += diff2 * diff2
	}

	snr1 := 10 * math.Log10(sumSqSig/sumSqErr1)
	snr2 := 10 * math.Log10(sumSqSig/sumSqErr2)

	t.Logf("\nSNR comparison:")
	t.Logf("  With sMid shifting: %.1f dB", snr1)
	t.Logf("  Direct (no sMid):   %.1f dB", snr2)

	if snr2 > snr1 {
		t.Logf("  >>> Direct approach is BETTER by %.1f dB!", snr2-snr1)
	} else {
		t.Logf("  >>> sMid approach is better by %.1f dB", snr1-snr2)
	}

	// Show first 30 samples comparison
	t.Logf("\nFirst 30 samples:")
	for i := 0; i < 30 && i < minLen; i++ {
		diff1 := output1[i] - libOut826[i]
		diff2 := output2[i] - libOut826[i]
		t.Logf("  [%2d] sMid=%+.6f direct=%+.6f lib=%+.6f  |  err_sMid=%+.6f err_direct=%+.6f",
			i, output1[i], output2[i], libOut826[i], diff1, diff2)
	}
}

// TestTV12AllPacketsSNRComparison compares SNR with and without sMid for multiple packets.
func TestTV12AllPacketsSNRComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 900)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create libopus 48kHz decoder
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Create gopus decoder for current approach
	goDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}

	t.Log("Comparing SNR for worst packets...")

	worstPackets := []int{826, 213, 137, 758, 825}

	for _, targetPkt := range worstPackets {
		if targetPkt >= len(packets) {
			continue
		}

		// Reset decoders
		libDec2, _ := NewLibopusDecoder(48000, 1)
		goDec2, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))

		var libOut, goOut []float32
		var libN int

		for i := 0; i <= targetPkt; i++ {
			pkt := packets[i]

			libPcm, n := libDec2.DecodeFloat(pkt, 1920)
			if i == targetPkt {
				libOut = libPcm
				libN = n
			}

			goPcm, _ := decodeFloat32(goDec2, pkt)
			if i == targetPkt {
				goOut = goPcm
			}
		}
		libDec2.Destroy()

		if len(libOut) == 0 || len(goOut) == 0 {
			continue
		}

		minLen := len(goOut)
		if libN < minLen {
			minLen = libN
		}

		var sumSqErr, sumSqSig float64
		for i := 0; i < minLen; i++ {
			libVal := float64(libOut[i])
			sumSqSig += libVal * libVal
			diff := float64(goOut[i]) - libVal
			sumSqErr += diff * diff
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)

		toc := gopus.ParseTOC(packets[targetPkt][0])
		t.Logf("Packet %d: Mode=%v BW=%v SNR=%.1f dB", targetPkt, toc.Mode, toc.Bandwidth, snr)
	}

	_ = goDec
}
