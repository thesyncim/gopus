//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for hybrid decoding.
package cgo

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestHybridDecodeVsLibopus compares hybrid mode decoding between gopus and libopus.
// This test is specifically for testvector05 and testvector06 which use hybrid mode.
func TestHybridDecodeVsLibopus(t *testing.T) {
	for _, tv := range []string{"testvector05", "testvector06"} {
		t.Run(tv, func(t *testing.T) {
			testHybridVector(t, tv)
		})
	}
}

func testHybridVector(t *testing.T, name string) {
	bitFile := fmt.Sprintf("../../../internal/testvectors/testdata/opus_testvectors/%s.bit", name)
	packets := loadPackets(t, bitFile, 0) // All packets
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2

	t.Logf("Testing %s: %d packets", name, len(packets))

	// Create persistent decoders for streaming
	goDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	defer libDec.Destroy()

	for i, pkt := range packets {
		// Decode with libopus
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples < 0 {
			t.Logf("Packet %d: libopus decode failed: %d", i, libSamples)
			continue
		}

		// Decode with gopus
		goPcm, decErr := decodeFloat32(goDec, pkt)
		if decErr != nil {
			t.Logf("Packet %d: gopus decode failed: %v", i, decErr)
			continue
		}

		// Check sample count
		goSamples := len(goPcm) / channels
		if goSamples != libSamples {
			t.Logf("Packet %d: sample count mismatch gopus=%d libopus=%d", i, goSamples, libSamples)
		}

		// Calculate SNR and show sample differences
		var sigPow, noisePow float64
		var maxDiff float64
		maxDiffIdx := 0
		for j := 0; j < minInt(goSamples*channels, libSamples*channels); j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			if math.Abs(noise) > maxDiff {
				maxDiff = math.Abs(noise)
				maxDiffIdx = j
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		// Parse TOC
		toc := gopus.ParseTOC(pkt[0])

		// Only log bad packets
		if snr < 20 {
			t.Logf("Packet %d: len=%d mode=%v stereo=%v fs=%d SNR=%.1fdB maxDiff=%.4f @%d",
				i, len(pkt), toc.Mode, toc.Stereo, toc.FrameSize, snr, maxDiff, maxDiffIdx)
			t.Logf("  First 10 samples comparison:")
			for j := 0; j < minInt(10, goSamples*channels); j++ {
				t.Logf("    [%d] gopus=%.6f libopus=%.6f diff=%.6f",
					j, goPcm[j], libPcm[j], goPcm[j]-libPcm[j])
			}
		}
	}
}

// TestHybridFirstPacketDetail analyzes the first hybrid packet in detail.
func TestHybridFirstPacketDetail(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector05.bit"
	packets := loadPackets(t, bitFile, 1)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	pkt := packets[0]
	channels := 2

	t.Logf("Packet: %d bytes", len(pkt))
	t.Logf("TOC: 0x%02X", pkt[0])

	// Print hex dump
	for i := 0; i < len(pkt); i += 16 {
		t.Logf("  %04X: ", i)
		end := i + 16
		if end > len(pkt) {
			end = len(pkt)
		}
		for j := i; j < end; j++ {
			fmt.Printf("%02X ", pkt[j])
		}
		fmt.Println()
	}

	// Decode with libopus - fresh decoder
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	libDec.Destroy()

	t.Logf("libopus: %d samples", libSamples)

	// Decode with gopus - fresh decoder
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	goPcm, decErr := decodeFloat32(goDec, pkt)
	if decErr != nil {
		t.Fatalf("gopus decode failed: %v", decErr)
	}

	goSamples := len(goPcm) / channels
	t.Logf("gopus: %d samples", goSamples)

	// Compare first and last samples
	t.Logf("First 20 samples:")
	for j := 0; j < minInt(20, goSamples*channels); j++ {
		ch := "L"
		if j%2 == 1 {
			ch = "R"
		}
		t.Logf("  [%4d] %s gopus=%10.6f libopus=%10.6f diff=%10.6f",
			j/2, ch, goPcm[j], libPcm[j], goPcm[j]-libPcm[j])
	}

	// Find first divergence point
	t.Logf("Looking for divergence point...")
	for j := 0; j < minInt(goSamples*channels, libSamples*channels); j++ {
		diff := math.Abs(float64(goPcm[j]) - float64(libPcm[j]))
		if diff > 0.001 { // Threshold for significant difference
			t.Logf("  First divergence at sample %d (frame sample %d, ch %d): diff=%.6f",
				j, j/channels, j%channels, diff)
			// Show surrounding samples
			start := maxInt(0, j-5)
			end := minInt(goSamples*channels, j+10)
			for k := start; k < end; k++ {
				marker := ""
				if k == j {
					marker = " <-- DIVERGES"
				}
				t.Logf("    [%d] gopus=%.6f libopus=%.6f diff=%.6f%s",
					k, goPcm[k], libPcm[k], goPcm[k]-libPcm[k], marker)
			}
			break
		}
	}

	// Calculate overall statistics
	var sigPow, noisePow float64
	var maxDiff float64
	maxDiffIdx := 0
	for j := 0; j < minInt(goSamples*channels, libSamples*channels); j++ {
		sig := float64(libPcm[j])
		noise := float64(goPcm[j]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
		if math.Abs(noise) > maxDiff {
			maxDiff = math.Abs(noise)
			maxDiffIdx = j
		}
	}

	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("Overall SNR: %.2f dB", snr)
	t.Logf("Max diff: %.6f at sample %d", maxDiff, maxDiffIdx)

	// Show samples around max diff
	t.Logf("Samples around max diff:")
	start := maxInt(0, maxDiffIdx-5)
	end := minInt(goSamples*channels, maxDiffIdx+10)
	for k := start; k < end; k++ {
		marker := ""
		if k == maxDiffIdx {
			marker = " <-- MAX DIFF"
		}
		t.Logf("  [%d] gopus=%.6f libopus=%.6f diff=%.6f%s",
			k, goPcm[k], libPcm[k], goPcm[k]-libPcm[k], marker)
	}
}

// TestHybridSilkCeltContributions tests SILK and CELT contributions separately.
func TestHybridSilkCeltContributions(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector05.bit"
	packets := loadPackets(t, bitFile, 5)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	pkt := packets[0]
	t.Logf("Analyzing packet 0: %d bytes, TOC=0x%02X", len(pkt), pkt[0])

	channels := 2
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	libDec.Destroy()

	if libSamples <= 0 {
		t.Fatalf("libopus decode failed")
	}

	t.Logf("libopus decoded %d samples", libSamples)

	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	goPcm, _ := decodeFloat32(goDec, pkt)
	goSamples := len(goPcm) / channels

	// Look at different regions of the output
	t.Logf("\n=== First 20 samples (delay region) ===")
	for j := 0; j < minInt(20, goSamples); j++ {
		goL := goPcm[j*2]
		goR := goPcm[j*2+1]
		libL := libPcm[j*2]
		libR := libPcm[j*2+1]
		diffL := goL - libL
		diffR := goR - libR
		t.Logf("[%3d] go:L=%9.6f R=%9.6f  lib:L=%9.6f R=%9.6f  diff:L=%9.6f R=%9.6f",
			j, goL, goR, libL, libR, diffL, diffR)
	}

	// Sample 60-80 (just after delay region)
	t.Logf("\n=== Samples 60-80 (post-delay) ===")
	for j := 60; j < minInt(80, goSamples); j++ {
		goL := goPcm[j*2]
		goR := goPcm[j*2+1]
		libL := libPcm[j*2]
		libR := libPcm[j*2+1]
		diffL := goL - libL
		diffR := goR - libR
		t.Logf("[%3d] go:L=%9.6f R=%9.6f  lib:L=%9.6f R=%9.6f  diff:L=%9.6f R=%9.6f",
			j, goL, goR, libL, libR, diffL, diffR)
	}

	// Samples around 315 (where most energy is in the frame)
	t.Logf("\n=== Samples 310-330 (mid-frame) ===")
	for j := 310; j < minInt(330, goSamples); j++ {
		goL := goPcm[j*2]
		goR := goPcm[j*2+1]
		libL := libPcm[j*2]
		libR := libPcm[j*2+1]
		diffL := goL - libL
		diffR := goR - libR
		t.Logf("[%3d] go:L=%9.6f R=%9.6f  lib:L=%9.6f R=%9.6f  diff:L=%9.6f R=%9.6f",
			j, goL, goR, libL, libR, diffL, diffR)
	}

	// Calculate per-region SNR
	regions := []struct {
		name  string
		start int
		end   int
	}{
		{"0-60 (delay)", 0, 60},
		{"60-120", 60, 120},
		{"120-240", 120, 240},
		{"240-480", 240, 480},
		{"480-720", 480, 720},
		{"720-960", 720, 960},
	}

	t.Logf("\n=== Per-region SNR ===")
	for _, r := range regions {
		if r.end > goSamples {
			continue
		}
		var sigPow, noisePow float64
		for j := r.start; j < r.end; j++ {
			for c := 0; c < channels; c++ {
				idx := j*channels + c
				sig := float64(libPcm[idx])
				noise := float64(goPcm[idx]) - sig
				sigPow += sig * sig
				noisePow += noise * noise
			}
		}
		snr := 10 * math.Log10(sigPow/noisePow)
		t.Logf("  %s: SNR=%.2f dB (sig=%.6f noise=%.6f)", r.name, snr, sigPow, noisePow)
	}
}

func loadPacketsForHybrid(t *testing.T, bitFile string, maxPackets int) [][]byte {
	t.Helper()

	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Logf("Cannot read %s: %v", bitFile, err)
		return nil
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		if maxPackets > 0 && len(packets) >= maxPackets {
			break
		}
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:]) // encFinal
		offset += 4

		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}

		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	return packets
}
