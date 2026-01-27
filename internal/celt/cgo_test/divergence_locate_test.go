// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestLocateDivergence finds exactly where libopus output diverges from reference.
func TestLocateDivergence(t *testing.T) {
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
	monoRef := make([]int16, len(reference)/2)
	for i := range monoRef {
		monoRef[i] = reference[i*2]
	}
	reference = monoRef

	t.Logf("Packets: %d, Reference samples (mono): %d", len(packets), len(reference))

	// Create PERSISTENT libopus decoder
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode all packets with persistent decoder and track divergence
	sampleOffset := 0
	firstDivergePkt := -1
	firstDivergeIdx := -1

	for pktIdx, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		libOutFloat, libN := libDec.DecodeFloat(pkt, toc.FrameSize*2)
		if libN <= 0 {
			continue
		}

		// Compare each sample
		for i := 0; i < libN; i++ {
			if sampleOffset+i >= len(reference) {
				break
			}
			libVal := int16(libOutFloat[i] * 32768.0)
			refVal := reference[sampleOffset+i]

			diff := int32(libVal) - int32(refVal)
			if diff != 0 {
				if firstDivergePkt < 0 {
					firstDivergePkt = pktIdx
					firstDivergeIdx = i
					t.Logf("FIRST DIVERGENCE at packet %d, sample %d (global sample %d)",
						pktIdx, i, sampleOffset+i)
					t.Logf("  lib=%d, ref=%d, diff=%d", libVal, refVal, diff)
				}
			}
		}

		// Calculate per-packet SNR
		var noise, signal float64
		for i := 0; i < libN && sampleOffset+i < len(reference); i++ {
			libVal := float64(libOutFloat[i]) * 32768.0
			refVal := float64(reference[sampleOffset+i])
			d := libVal - refVal
			noise += d * d
			signal += refVal * refVal
		}
		snr := 10 * math.Log10(signal/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		if snr < 100 {
			t.Logf("Packet %d: SNR=%.2f dB (samples %d-%d)", pktIdx, snr, sampleOffset, sampleOffset+libN)
			// Show first few divergent samples
			count := 0
			for i := 0; i < libN && count < 5; i++ {
				if sampleOffset+i >= len(reference) {
					break
				}
				libVal := int16(libOutFloat[i] * 32768.0)
				refVal := reference[sampleOffset+i]
				if libVal != refVal {
					t.Logf("  [%d] lib=%d ref=%d diff=%d", sampleOffset+i, libVal, refVal, int(libVal)-int(refVal))
					count++
				}
			}
		}

		sampleOffset += libN
	}

	t.Logf("\nTotal samples decoded: %d", sampleOffset)
	t.Logf("Reference samples: %d", len(reference))

	if firstDivergePkt >= 0 {
		t.Logf("\nDivergence starts at packet %d, local sample %d", firstDivergePkt, firstDivergeIdx)
	} else {
		t.Log("\nNo divergence found - all samples match!")
	}
}

// TestCompareFreshVsPersistent compares libopus output with fresh vs persistent decoder.
func TestCompareFreshVsPersistent(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"

	packets, err := loadPacketsSimple(bitFile, 20) // Just first 20 packets
	if err != nil {
		t.Skip("Could not load packets:", err)
	}

	t.Logf("Testing %d packets", len(packets))

	// Create persistent decoder
	persistentDec, _ := NewLibopusDecoder(48000, 1)
	if persistentDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer persistentDec.Destroy()

	for pktIdx, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])

		// Decode with persistent decoder
		persistentOut, persistentN := persistentDec.DecodeFloat(pkt, toc.FrameSize*2)
		if persistentN <= 0 {
			continue
		}

		// Decode same packet with fresh decoder
		freshDec, _ := NewLibopusDecoder(48000, 1)
		if freshDec == nil {
			continue
		}
		freshOut, freshN := freshDec.DecodeFloat(pkt, toc.FrameSize*2)
		freshDec.Destroy()

		if freshN != persistentN {
			t.Logf("Packet %d: length mismatch fresh=%d, persistent=%d", pktIdx, freshN, persistentN)
			continue
		}

		// Compare
		var noise, signal float64
		diffs := 0
		for i := 0; i < freshN; i++ {
			pVal := persistentOut[i]
			fVal := freshOut[i]
			d := float64(pVal - fVal)
			noise += d * d
			signal += float64(fVal * fVal)
			if pVal != fVal {
				diffs++
			}
		}

		snr := 10 * math.Log10(signal/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		if snr < 100 {
			t.Logf("Packet %d: fresh vs persistent SNR=%.2f dB (%d samples differ)", pktIdx, snr, diffs)
		} else {
			t.Logf("Packet %d: MATCH (SNR=%.2f dB)", pktIdx, snr)
		}
	}
}
