// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestReferenceAnalysis does a deeper analysis of the reference file divergence.
// This helps understand why libopus output differs from the reference .dec file.
func TestReferenceAnalysis(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	decFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.dec"

	packets, err := loadPacketsSimple(bitFile, -1)
	if err != nil {
		t.Skip("Could not load packets:", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skip("Could not load reference:", err)
	}

	// Reference is stereo - extract left channel
	if len(reference)%2 == 0 {
		monoRef := make([]int16, len(reference)/2)
		for i := range monoRef {
			monoRef[i] = reference[i*2]
		}
		reference = monoRef
	}

	t.Logf("Packets: %d, Reference samples: %d", len(packets), len(reference))

	// Create libopus decoder
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode all packets
	var libSamples []int16
	for _, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		libOutFloat, libN := libDec.DecodeFloat(pkt, toc.FrameSize*2)
		if libN > 0 {
			for i := 0; i < libN; i++ {
				v := libOutFloat[i] * 32768.0
				if v > 32767 {
					v = 32767
				} else if v < -32768 {
					v = -32768
				}
				libSamples = append(libSamples, int16(v))
			}
		}
	}

	t.Logf("libopus decoded: %d samples", len(libSamples))

	// Find first non-zero sample
	firstNonZero := -1
	for i, s := range reference {
		if s != 0 {
			firstNonZero = i
			break
		}
	}
	t.Logf("First non-zero reference sample at index: %d", firstNonZero)

	// Also check libopus first non-zero
	libFirstNonZero := -1
	for i, s := range libSamples {
		if s != 0 {
			libFirstNonZero = i
			break
		}
	}
	t.Logf("First non-zero libopus sample at index: %d", libFirstNonZero)

	// Show samples around first non-zero
	if firstNonZero > 0 && firstNonZero < len(libSamples) {
		start := firstNonZero - 5
		if start < 0 {
			start = 0
		}
		end := firstNonZero + 20
		if end > len(reference) {
			end = len(reference)
		}
		if end > len(libSamples) {
			end = len(libSamples)
		}

		t.Log("\n=== Samples around first non-zero ===")
		for i := start; i < end; i++ {
			diff := int(libSamples[i]) - int(reference[i])
			t.Logf("  [%d] lib=%7d ref=%7d diff=%6d", i, libSamples[i], reference[i], diff)
		}
	}

	// Try delay alignment
	t.Log("\n=== Delay Alignment Analysis ===")
	minLen := len(libSamples)
	if len(reference) < minLen {
		minLen = len(reference)
	}

	// Test delays from -100 to +100 samples
	bestDelay := 0
	bestSNR := -999.0
	for delay := -100; delay <= 100; delay++ {
		var noise, signal float64
		count := 0
		for i := 0; i < minLen-100; i++ {
			refIdx := i
			libIdx := i + delay
			if libIdx < 0 || libIdx >= len(libSamples) {
				continue
			}
			diff := float64(libSamples[libIdx]) - float64(reference[refIdx])
			noise += diff * diff
			signal += float64(reference[refIdx]) * float64(reference[refIdx])
			count++
		}
		if count > 0 {
			snr := 10 * math.Log10(signal/noise)
			if snr > bestSNR {
				bestSNR = snr
				bestDelay = delay
			}
		}
	}
	t.Logf("Best delay: %d samples, SNR: %.2f dB", bestDelay, bestSNR)

	// Try comparing per-packet
	t.Log("\n=== Per-Packet Comparison ===")
	libDec.Destroy()
	libDec2, _ := NewLibopusDecoder(48000, 1)
	if libDec2 == nil {
		t.Skip("Could not create second libopus decoder")
	}
	defer libDec2.Destroy()

	refOffset := 0
	for pktIdx := 0; pktIdx < 10 && pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		expectedSamples := toc.FrameSize

		// Decode this packet
		libOutFloat, libN := libDec2.DecodeFloat(pkt, expectedSamples*2)
		if libN <= 0 {
			t.Logf("Packet %d: decode failed", pktIdx)
			continue
		}

		// Get reference samples for this packet (stereo, so *2)
		stereoRefStart := refOffset * 2
		stereoRefEnd := stereoRefStart + expectedSamples*2
		if stereoRefEnd > len(reference)*2 {
			break
		}

		// Compare
		var noise, signal float64
		for i := 0; i < libN && i < expectedSamples; i++ {
			libVal := float64(libOutFloat[i]) * 32768.0
			refVal := float64(reference[refOffset+i])
			diff := libVal - refVal
			noise += diff * diff
			signal += refVal * refVal
		}
		snr := 10 * math.Log10(signal/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		t.Logf("Packet %d: frameSize=%d, libN=%d, SNR=%.2f dB",
			pktIdx, expectedSamples, libN, snr)

		refOffset += expectedSamples
	}

	// Check if libopus float uses different scaling
	t.Log("\n=== Scaling Analysis ===")
	if len(libSamples) > 1000 && len(reference) > 1000 {
		// Find a region with significant signal
		signalStart := -1
		for i := 500; i < len(reference)-100; i++ {
			if math.Abs(float64(reference[i])) > 1000 {
				signalStart = i
				break
			}
		}
		if signalStart > 0 {
			t.Logf("Found signal region at sample %d", signalStart)
			var libSum, refSum float64
			for i := signalStart; i < signalStart+100; i++ {
				libSum += math.Abs(float64(libSamples[i]))
				refSum += math.Abs(float64(reference[i]))
			}
			t.Logf("Average magnitude - lib: %.2f, ref: %.2f, ratio: %.4f",
				libSum/100, refSum/100, libSum/refSum)
		}
	}
}
