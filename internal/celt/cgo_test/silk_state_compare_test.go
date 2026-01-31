// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkStateCompare compares internal state between gopus and libopus
// at the exact divergence point (packet 4, frame 2, k=0).
func TestSilkStateCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	pkt0 := packets[0]
	toc := gopus.ParseTOC(pkt0[0])
	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	config := silk.GetBandwidthConfig(silkBW)
	delay := 5

	// Create persistent decoders
	goDec := silk.NewDecoder()
	libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDec == nil {
		t.Fatal("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode packets 0-3 (all bit-exact)
	for pktIdx := 0; pktIdx < 4; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		_, err := goDec.DecodeFrame(&rd, silkBW, duration, pktIdx == 0)
		if err != nil {
			t.Fatalf("gopus decode failed for packet %d: %v", pktIdx, err)
		}

		_, libSamples := libDec.DecodeFloat(pkt, 960)
		if libSamples < 0 {
			t.Fatalf("libopus decode failed for packet %d", pktIdx)
		}
	}

	t.Log("Packets 0-3 decoded (bit-exact)")

	// Now decode packet 4 and compare frame by frame
	pkt4 := packets[4]
	toc = gopus.ParseTOC(pkt4[0])
	silkBW, _ = silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	var rd rangecoding.Decoder
	rd.Init(pkt4[1:])
	goOutput, err := goDec.DecodeFrame(&rd, silkBW, duration, false)
	if err != nil {
		t.Fatalf("gopus decode failed for packet 4: %v", err)
	}

	libPcm, libSamples := libDec.DecodeFloat(pkt4, 960)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed for packet 4")
	}

	// Analyze frame by frame
	sampleRate := config.SampleRate
	samplesPerFrame := int(duration) * sampleRate / 1000 / 3 // 3 frames per 60ms packet

	t.Logf("\nPacket 4 analysis (sample rate=%d, samples/frame=%d):", sampleRate, samplesPerFrame)

	for frame := 0; frame < 3; frame++ {
		startSample := frame * samplesPerFrame
		endSample := startSample + samplesPerFrame
		if endSample > len(goOutput) {
			endSample = len(goOutput)
		}

		diffs := 0
		firstDiff := -1
		maxDiff := 0
		for i := startSample; i < endSample; i++ {
			goVal := int(goOutput[i] * 32768)
			libVal := int(libPcm[i+delay] * 32768)
			diff := goVal - libVal
			if diff != 0 {
				diffs++
				if firstDiff < 0 {
					firstDiff = i - startSample
				}
				if diff < 0 {
					diff = -diff
				}
				if diff > maxDiff {
					maxDiff = diff
				}
			}
		}

		t.Logf("  Frame %d [%d-%d]: diffs=%d, firstDiff=%d, maxDiff=%d",
			frame, startSample, endSample-1, diffs, firstDiff, maxDiff)

		if frame == 2 && firstDiff >= 0 {
			// Show first few samples of Frame 2
			t.Log("  Frame 2 first 10 samples:")
			for i := 0; i < 10 && startSample+i < endSample; i++ {
				goVal := int(goOutput[startSample+i] * 32768)
				libVal := int(libPcm[startSample+i+delay] * 32768)
				diff := goVal - libVal
				marker := ""
				if diff != 0 {
					marker = " <-- DIFF"
				}
				t.Logf("    [%d] go=%d, lib=%d, diff=%d%s",
					startSample+i, goVal, libVal, diff, marker)
			}
		}
	}

	// Get decoder state for debugging
	params := goDec.GetLastFrameParams()
	sigType := goDec.GetLastSignalType()
	t.Logf("\nFrame 2 (last decoded) parameters:")
	t.Logf("  SignalType: %d (2=voiced)", sigType)
	t.Logf("  NLSFInterpCoefQ2: %d (4=no interp, <4=interp)", params.NLSFInterpCoefQ2)
	t.Logf("  LTPScaleIndex: %d", params.LTPScaleIndex)
	t.Logf("  GainIndices: %v", params.GainIndices)
}

// TestSilkFreshVsState compares fresh decoder vs decoder with state
func TestSilkFreshVsState(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	pkt4 := packets[4]
	toc := gopus.ParseTOC(pkt4[0])
	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	config := silk.GetBandwidthConfig(silkBW)
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	delay := 5

	// Test 1: Fresh gopus decoder
	goDecFresh := silk.NewDecoder()
	var rdFresh rangecoding.Decoder
	rdFresh.Init(pkt4[1:])
	goFreshOutput, err := goDecFresh.DecodeFrame(&rdFresh, silkBW, duration, true)
	if err != nil {
		t.Fatalf("Fresh gopus decode failed: %v", err)
	}

	// Test 2: Fresh libopus decoder
	libDecFresh, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDecFresh == nil {
		t.Fatal("Could not create fresh libopus decoder")
	}
	libFreshPcm, libFreshSamples := libDecFresh.DecodeFloat(pkt4, 960)
	libDecFresh.Destroy()

	// Compare fresh decoders
	t.Log("Fresh decoder comparison (packet 4 only):")
	alignedLen := len(goFreshOutput)
	if libFreshSamples-delay < alignedLen {
		alignedLen = libFreshSamples - delay
	}

	exactMatches := 0
	firstMismatch := -1
	for i := 0; i < alignedLen; i++ {
		goVal := int(goFreshOutput[i] * 32768)
		libVal := int(libFreshPcm[i+delay] * 32768)
		if goVal == libVal {
			exactMatches++
		} else if firstMismatch < 0 {
			firstMismatch = i
		}
	}

	t.Logf("  Exact matches: %d/%d (%.1f%%)", exactMatches, alignedLen,
		100.0*float64(exactMatches)/float64(alignedLen))
	if firstMismatch >= 0 {
		t.Logf("  First mismatch at sample %d", firstMismatch)

		// Show context
		start := firstMismatch - 2
		if start < 0 {
			start = 0
		}
		end := firstMismatch + 8
		if end > alignedLen {
			end = alignedLen
		}
		for i := start; i < end; i++ {
			goVal := int(goFreshOutput[i] * 32768)
			libVal := int(libFreshPcm[i+delay] * 32768)
			diff := goVal - libVal
			marker := ""
			if i == firstMismatch {
				marker = " <-- FIRST"
			}
			t.Logf("    [%d] go=%d, lib=%d, diff=%d%s", i, goVal, libVal, diff, marker)
		}
	}
}
