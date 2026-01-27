// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkPacket4Debug investigates why packet 4 has lower match rate.
func TestSilkPacket4Debug(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	pkt := packets[4]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 4: TOC=0x%02X, Mode=%d, Bandwidth=%d, FrameSize=%d",
		pkt[0], toc.Mode, toc.Bandwidth, toc.FrameSize)

	if toc.Mode != gopus.ModeSILK {
		t.Skip("Not SILK mode")
	}

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	config := silk.GetBandwidthConfig(silkBW)

	t.Logf("SILK BW=%d (%d kHz), Duration=%d ms", silkBW, config.SampleRate/1000, duration)

	delay := 5 // for 8kHz

	// Decode with FRESH decoder (no state carryover)
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()
	goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDec == nil {
		t.Fatal("Could not create libopus decoder")
	}
	libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
	libDec.Destroy()

	t.Logf("gopus samples: %d, libopus samples: %d", len(goNative), libSamples)

	alignedLen := len(goNative)
	if libSamples-delay < alignedLen {
		alignedLen = libSamples - delay
	}

	// Find first mismatch
	firstMismatch := -1
	var diffStats [5]int // count diffs by magnitude: 0, 1, 2-10, 11-100, >100
	for i := 0; i < alignedLen; i++ {
		goVal := goNative[i]
		libVal := libPcm[i+delay]
		goInt := int(goVal * 32768)
		libInt := int(libVal * 32768)
		diff := goInt - libInt

		if diff != 0 && firstMismatch < 0 {
			firstMismatch = i
		}

		absDiff := diff
		if absDiff < 0 {
			absDiff = -absDiff
		}
		switch {
		case absDiff == 0:
			diffStats[0]++
		case absDiff == 1:
			diffStats[1]++
		case absDiff <= 10:
			diffStats[2]++
		case absDiff <= 100:
			diffStats[3]++
		default:
			diffStats[4]++
		}
	}

	t.Logf("\nDifference distribution:")
	t.Logf("  Exact (0): %d (%.1f%%)", diffStats[0], 100.0*float64(diffStats[0])/float64(alignedLen))
	t.Logf("  1 LSB:     %d (%.1f%%)", diffStats[1], 100.0*float64(diffStats[1])/float64(alignedLen))
	t.Logf("  2-10:      %d (%.1f%%)", diffStats[2], 100.0*float64(diffStats[2])/float64(alignedLen))
	t.Logf("  11-100:    %d (%.1f%%)", diffStats[3], 100.0*float64(diffStats[3])/float64(alignedLen))
	t.Logf("  >100:      %d (%.1f%%)", diffStats[4], 100.0*float64(diffStats[4])/float64(alignedLen))

	if firstMismatch >= 0 {
		t.Logf("\nFirst mismatch at sample %d", firstMismatch)
		// Show context around first mismatch
		start := firstMismatch - 5
		if start < 0 {
			start = 0
		}
		end := firstMismatch + 15
		if end > alignedLen {
			end = alignedLen
		}
		for i := start; i < end; i++ {
			goVal := goNative[i]
			libVal := libPcm[i+delay]
			goInt := int(goVal * 32768)
			libInt := int(libVal * 32768)
			diff := goInt - libInt
			marker := ""
			if i == firstMismatch {
				marker = " <-- FIRST MISMATCH"
			}
			t.Logf("  [%3d] go=%6d lib=%6d diff=%4d%s", i, goInt, libInt, diff, marker)
		}
	}

	// Check if mismatches are at subframe boundaries
	t.Log("\nSubframe boundary analysis (40 samples at 8kHz for 5ms):")
	for sf := 0; sf < 12 && sf*40 < alignedLen; sf++ {
		start := sf * 40
		end := start + 40
		if end > alignedLen {
			end = alignedLen
		}
		var sfErr float64
		var sfSig float64
		mismatches := 0
		for i := start; i < end; i++ {
			goVal := goNative[i]
			libVal := libPcm[i+delay]
			diff := goVal - libVal
			sfErr += float64(diff * diff)
			sfSig += float64(libVal * libVal)
			if diff != 0 {
				mismatches++
			}
		}
		snr := 10 * math.Log10(sfSig/sfErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}
		t.Logf("  Subframe %2d [%3d-%3d]: mismatches=%2d, SNR=%.1f dB",
			sf, start, end-1, mismatches, snr)
	}
}

// TestSilkPacket4WithState tests packet 4 with proper state from previous packets.
func TestSilkPacket4WithState(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	// Create persistent decoders
	pkt0 := packets[0]
	toc := gopus.ParseTOC(pkt0[0])
	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	config := silk.GetBandwidthConfig(silkBW)
	delay := 5

	// gopus decoder with state
	goDec := silk.NewDecoder()

	// libopus decoder with state
	libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDec == nil {
		t.Fatal("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("Decoding packets 0-4 with persistent state:")

	for pktIdx := 0; pktIdx <= 4; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// gopus decode
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, pktIdx == 0)
		if err != nil {
			t.Fatalf("gopus decode failed for packet %d: %v", pktIdx, err)
		}

		// libopus decode
		libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
		if libSamples < 0 {
			t.Fatalf("libopus decode failed for packet %d", pktIdx)
		}

		alignedLen := len(goNative)
		if libSamples-delay < alignedLen {
			alignedLen = libSamples - delay
		}

		exactMatches := 0
		var sumSqErr, sumSqSig float64
		for i := 0; i < alignedLen; i++ {
			goVal := goNative[i]
			libVal := libPcm[i+delay]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff == 0 {
				exactMatches++
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("  Packet %d: aligned SNR=%.1f dB, exact=%.1f%% (%d/%d)",
			pktIdx, snr, 100.0*float64(exactMatches)/float64(alignedLen),
			exactMatches, alignedLen)
	}
}
