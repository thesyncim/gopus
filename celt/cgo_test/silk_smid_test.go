//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestSilkSMidBuffering compares full decode paths (with sMid) to verify
// the difference is in sMid handling, not core decoding.
func TestSilkSMidBuffering(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadSMidPackets(t, bitFile, 5)
	if len(packets) < 2 {
		t.Skip("Could not load enough test packets")
	}

	channels := 1

	// Create decoders at 48kHz (both with full resampling)
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	for pktIdx := 0; pktIdx < 2; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		t.Logf("\n=== Packet %d: %d bytes, frameSize=%d ===", pktIdx, len(pkt), toc.FrameSize)

		// Decode with gopus (full path with sMid buffering and resampling)
		goPcm, goErr := decodeFloat32(goDec, pkt)
		if goErr != nil {
			t.Fatalf("gopus decode failed: %v", goErr)
		}

		// Decode with libopus (full path)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples < 0 {
			t.Fatalf("libopus decode failed: %d", libSamples)
		}

		// Compare first 20 samples
		t.Log("First 20 samples comparison (48kHz output):")
		minLen := len(goPcm)
		if libSamples < minLen {
			minLen = libSamples
		}
		if minLen > 20 {
			minLen = 20
		}

		maxDiff := float32(0)
		for i := 0; i < minLen; i++ {
			diff := goPcm[i] - libPcm[i]
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
			}
			t.Logf("  [%d] go=%.6f lib=%.6f diff=%.6f", i, goPcm[i], libPcm[i], goPcm[i]-libPcm[i])
		}

		// Find first divergence
		firstDiff := -1
		for i := 0; i < len(goPcm) && i < libSamples; i++ {
			diff := goPcm[i] - libPcm[i]
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.0001 {
				firstDiff = i
				break
			}
		}
		t.Logf("First divergence at sample: %d (max diff in first 20: %.6f)", firstDiff, maxDiff)
	}
}

// TestSilkNativeVsAPI compares gopus native output with libopus at different API rates.
func TestSilkNativeVsAPI(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadSMidPackets(t, bitFile, 1)
	if len(packets) < 1 {
		t.Skip("Could not load packets")
	}

	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 0: TOC=0x%02X, Bandwidth=%d, FrameSize=%d", pkt[0], toc.Bandwidth, toc.FrameSize)

	// Decode with libopus at different API rates
	rates := []int{8000, 16000, 48000}
	for _, rate := range rates {
		libDec, err := NewLibopusDecoder(rate, 1)
		if err != nil || libDec == nil {
			t.Logf("Could not create decoder at %d Hz", rate)
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		libDec.Destroy()

		if libSamples < 0 {
			t.Logf("Decode failed at %d Hz", rate)
			continue
		}

		// Show first 10 samples
		t.Logf("\nlibopus at %d Hz (%d samples):", rate, libSamples)
		for i := 0; i < 10 && i < libSamples; i++ {
			t.Logf("  [%d] %.6f", i, libPcm[i])
		}
	}
}

func loadSMidPackets(t *testing.T, bitFile string, maxPackets int) [][]byte {
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
		_ = binary.BigEndian.Uint32(data[offset:])
		offset += 4

		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}

		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	return packets
}
