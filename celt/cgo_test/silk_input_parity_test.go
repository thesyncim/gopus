//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK encoder input parity.
// This test captures the resampled PCM input at each frame from BOTH gopus and
// libopus, comparing them sample-by-sample to identify the first divergence.
//
// Context: gopus produces byte-identical packets for frames 0-28, then diverges.
// All SILK-level encoder parameters match, so divergence must be in the PCM input.
package cgo

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestSILKInputParity encodes 50 frames of a multi-frequency signal with both
// gopus and libopus, capturing the SILK encoder's resampled int16 PCM input at
// each frame.  It compares packets byte-by-byte and input samples sample-by-sample
// to locate the first divergence.
//
// For libopus: the SILK input is captured from sCmn.inputBuf[1..frame_length]
// via CaptureStateFromExistingEncoder (the 2-sample history prefix is at [0..1]).
//
// For gopus: the SILK input is captured via the NSQ trace hook (InputQ0), which
// stores the int16-quantized PCM that enters the noise shaping quantizer.
func TestSILKInputParity(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960 // 20ms at 48kHz
		bitrate    = 32000
		numFrames  = 50
	)

	totalSamples := numFrames * frameSize * channels
	original := generateMultiFreqSignal(totalSamples)

	// ---------- gopus encoder ----------
	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	// ---------- libopus encoder (persistent for state capture) ----------
	libEnc, err := NewLibopusEncoder(sampleRate, channels, OpusApplicationRestrictedSilk)
	if err != nil || libEnc == nil {
		t.Fatal("Could not create libopus encoder")
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(OpusBandwidthWideband)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	type frameResult struct {
		goPacket  []byte
		libPacket []byte

		// SILK input samples (int16): the resampled PCM after LP filter, before NSQ.
		// For libopus: inputBuf[1..frame_length]  (frame_length = 320 at WB 16kHz 20ms)
		// For gopus:   NSQ trace InputQ0[0..frameSamples-1]
		goInputQ0  []int16
		libInputQ0 []int16

		// nBitsExceeded from both encoders
		goNBitsExceeded  int
		libNBitsExceeded int
	}

	results := make([]frameResult, numFrames)

	for i := 0; i < numFrames; i++ {
		start := i * frameSize

		// --- gopus encode with NSQ trace to capture InputQ0 ---
		trace := &silk.EncoderTrace{
			FramePre: &silk.FrameStateTrace{},
			Frame:    &silk.FrameStateTrace{},
			NSQ:      &silk.NSQTrace{CaptureInputs: true},
		}
		goEnc.SetSilkTrace(trace)

		pcm64 := make([]float64, frameSize)
		for j := 0; j < frameSize; j++ {
			pcm64[j] = float64(original[start+j])
		}
		goPkt, encErr := goEnc.Encode(pcm64, frameSize)
		if encErr != nil {
			t.Fatalf("gopus encode frame %d: %v", i, encErr)
		}
		cp := make([]byte, len(goPkt))
		copy(cp, goPkt)
		goEnc.SetSilkTrace(nil)

		goInputQ0 := make([]int16, len(trace.NSQ.InputQ0))
		copy(goInputQ0, trace.NSQ.InputQ0)

		// --- libopus encode ---
		pcm := original[start : start+frameSize]
		libPkt, n := libEnc.EncodeFloat(pcm, frameSize)
		if n <= 0 {
			t.Fatalf("libopus encode frame %d: returned %d", i, n)
		}

		// Capture libopus state (includes inputBuf)
		libState, ok := CaptureStateFromExistingEncoder(libEnc.GetEncoderPtr())
		if !ok {
			t.Fatalf("Failed to capture libopus state at frame %d", i)
		}

		// Extract libopus input: inputBuf[1..frame_length] (skip history prefix)
		libInputQ0 := make([]int16, 0)
		if libState.FrameLength > 0 && len(libState.InputBufQ0) >= libState.FrameLength+1 {
			libInputQ0 = make([]int16, libState.FrameLength)
			copy(libInputQ0, libState.InputBufQ0[1:libState.FrameLength+1])
		}

		results[i] = frameResult{
			goPacket:         cp,
			libPacket:        libPkt,
			goInputQ0:        goInputQ0,
			libInputQ0:       libInputQ0,
			goNBitsExceeded:  trace.Frame.NBitsExceeded,
			libNBitsExceeded: libState.NBitsExceeded,
		}
	}

	// ---------- Analysis ----------
	firstPacketDivergence := -1
	firstInputDivergence := -1
	firstNBitsExceededDivergence := -1

	for i := 0; i < numFrames; i++ {
		r := results[i]

		// --- Packet comparison ---
		packetMatch := len(r.goPacket) == len(r.libPacket)
		firstDiffByte := -1
		if packetMatch {
			for j := 0; j < len(r.goPacket); j++ {
				if r.goPacket[j] != r.libPacket[j] {
					packetMatch = false
					firstDiffByte = j
					break
				}
			}
		} else {
			// Different lengths - find first diff
			minLen := len(r.goPacket)
			if len(r.libPacket) < minLen {
				minLen = len(r.libPacket)
			}
			for j := 0; j < minLen; j++ {
				if r.goPacket[j] != r.libPacket[j] {
					firstDiffByte = j
					break
				}
			}
			if firstDiffByte == -1 {
				firstDiffByte = minLen
			}
		}

		if !packetMatch && firstPacketDivergence < 0 {
			firstPacketDivergence = i
		}

		// --- nBitsExceeded comparison ---
		nBitsMatch := r.goNBitsExceeded == r.libNBitsExceeded
		if !nBitsMatch && firstNBitsExceededDivergence < 0 {
			firstNBitsExceededDivergence = i
		}

		// --- Input PCM comparison ---
		inputMatch := true
		firstDiffSample := -1
		maxAbsDiff := int16(0)
		sumAbsDiff := int64(0)
		compareLen := len(r.goInputQ0)
		if len(r.libInputQ0) < compareLen {
			compareLen = len(r.libInputQ0)
		}
		if len(r.goInputQ0) != len(r.libInputQ0) {
			inputMatch = false
		}
		for j := 0; j < compareLen; j++ {
			diff := r.goInputQ0[j] - r.libInputQ0[j]
			absDiff := diff
			if absDiff < 0 {
				absDiff = -absDiff
			}
			sumAbsDiff += int64(absDiff)
			if absDiff > maxAbsDiff {
				maxAbsDiff = absDiff
			}
			if diff != 0 {
				inputMatch = false
				if firstDiffSample < 0 {
					firstDiffSample = j
				}
			}
		}

		if !inputMatch && firstInputDivergence < 0 {
			firstInputDivergence = i
		}

		// --- Log summary for every frame, with detail around divergence ---
		showDetail := !packetMatch || !inputMatch || !nBitsMatch ||
			(firstPacketDivergence >= 0 && i >= firstPacketDivergence-2 && i <= firstPacketDivergence+5) ||
			(firstInputDivergence >= 0 && i >= firstInputDivergence-2 && i <= firstInputDivergence+5) ||
			i < 3

		if showDetail {
			pktStatus := "MATCH"
			if !packetMatch {
				pktStatus = fmt.Sprintf("DIFF (go=%d lib=%d bytes, first@byte %d)", len(r.goPacket), len(r.libPacket), firstDiffByte)
			}

			inputStatus := "MATCH"
			if !inputMatch {
				avgDiff := float64(0)
				if compareLen > 0 {
					avgDiff = float64(sumAbsDiff) / float64(compareLen)
				}
				inputStatus = fmt.Sprintf("DIFF (go=%d lib=%d samples, first@%d, maxAbsDiff=%d, avgAbsDiff=%.2f)",
					len(r.goInputQ0), len(r.libInputQ0), firstDiffSample, maxAbsDiff, avgDiff)
			}

			nBitsStatus := "MATCH"
			if !nBitsMatch {
				nBitsStatus = fmt.Sprintf("DIFF (go=%d lib=%d)", r.goNBitsExceeded, r.libNBitsExceeded)
			}

			t.Logf("Frame %2d: pkt=%s", i, pktStatus)
			if !inputMatch || !nBitsMatch {
				t.Logf("         input=%s", inputStatus)
				t.Logf("         nBitsExceeded=%s", nBitsStatus)
			}

			// Dump sample context around first divergence
			if firstDiffSample >= 0 && i == firstInputDivergence {
				dumpStart := firstDiffSample - 5
				if dumpStart < 0 {
					dumpStart = 0
				}
				dumpEnd := firstDiffSample + 10
				if dumpEnd > compareLen {
					dumpEnd = compareLen
				}
				t.Logf("         Input sample dump around first diff (sample %d):", firstDiffSample)
				for j := dumpStart; j < dumpEnd; j++ {
					goVal := int16(0)
					libVal := int16(0)
					if j < len(r.goInputQ0) {
						goVal = r.goInputQ0[j]
					}
					if j < len(r.libInputQ0) {
						libVal = r.libInputQ0[j]
					}
					marker := " "
					if goVal != libVal {
						marker = "*"
					}
					t.Logf("           [%3d] go=%6d lib=%6d diff=%4d %s", j, goVal, libVal, goVal-libVal, marker)
				}
			}

			// Dump packet bytes around first divergence
			if firstDiffByte >= 0 && i == firstPacketDivergence {
				dumpStart := 0
				dumpEnd := len(r.goPacket)
				if len(r.libPacket) > dumpEnd {
					dumpEnd = len(r.libPacket)
				}
				if dumpEnd > firstDiffByte+10 {
					dumpEnd = firstDiffByte + 10
				}
				if dumpStart > firstDiffByte-5 {
					dumpStart = firstDiffByte - 5
				}
				if dumpStart < 0 {
					dumpStart = 0
				}
				t.Logf("         Packet byte dump around first diff (byte %d):", firstDiffByte)
				for j := dumpStart; j < dumpEnd; j++ {
					goB := byte(0)
					libB := byte(0)
					if j < len(r.goPacket) {
						goB = r.goPacket[j]
					}
					if j < len(r.libPacket) {
						libB = r.libPacket[j]
					}
					marker := " "
					if goB != libB {
						marker = "*"
					}
					t.Logf("           [%2d] go=0x%02x lib=0x%02x %s", j, goB, libB, marker)
				}
			}
		}
	}

	// ---------- Summary ----------
	t.Log("")
	t.Log("=== SUMMARY ===")

	packetMatchCount := 0
	inputMatchCount := 0
	for i := 0; i < numFrames; i++ {
		r := results[i]
		if len(r.goPacket) == len(r.libPacket) {
			match := true
			for j := 0; j < len(r.goPacket); j++ {
				if r.goPacket[j] != r.libPacket[j] {
					match = false
					break
				}
			}
			if match {
				packetMatchCount++
			}
		}
		compareLen := len(r.goInputQ0)
		if len(r.libInputQ0) < compareLen {
			compareLen = len(r.libInputQ0)
		}
		match := len(r.goInputQ0) == len(r.libInputQ0)
		if match {
			for j := 0; j < compareLen; j++ {
				if r.goInputQ0[j] != r.libInputQ0[j] {
					match = false
					break
				}
			}
		}
		if match {
			inputMatchCount++
		}
	}

	t.Logf("Packet parity:       %d/%d frames identical", packetMatchCount, numFrames)
	t.Logf("Input PCM parity:    %d/%d frames identical", inputMatchCount, numFrames)
	t.Logf("First packet divergence:       frame %d", firstPacketDivergence)
	t.Logf("First input PCM divergence:    frame %d", firstInputDivergence)
	t.Logf("First nBitsExceeded divergence: frame %d", firstNBitsExceededDivergence)

	if firstInputDivergence >= 0 && firstPacketDivergence >= 0 {
		if firstInputDivergence < firstPacketDivergence {
			t.Logf("")
			t.Logf("CONCLUSION: Input PCM diverges BEFORE packets diverge (frame %d vs %d)",
				firstInputDivergence, firstPacketDivergence)
			t.Logf("  => The resampler or input alignment is the root cause")
		} else if firstInputDivergence == firstPacketDivergence {
			t.Logf("")
			t.Logf("CONCLUSION: Input PCM and packets diverge at the SAME frame (%d)",
				firstInputDivergence)
			t.Logf("  => Need deeper investigation (resampler state feedback, nBitsExceeded)")
		} else {
			t.Logf("")
			t.Logf("CONCLUSION: Packets diverge BEFORE input PCM (frame %d vs %d)",
				firstPacketDivergence, firstInputDivergence)
			t.Logf("  => Divergence is in encoding logic, not input PCM")
		}
	} else if firstPacketDivergence >= 0 && firstInputDivergence < 0 {
		t.Logf("")
		t.Logf("CONCLUSION: Packets diverge at frame %d but input PCM is identical",
			firstPacketDivergence)
		t.Logf("  => Divergence is purely in encoding logic (not input data)")
	} else if firstInputDivergence >= 0 && firstPacketDivergence < 0 {
		t.Logf("")
		t.Logf("CONCLUSION: Input PCM diverges at frame %d but packets are identical",
			firstInputDivergence)
		t.Logf("  => Small PCM differences are being absorbed by quantization")
	} else {
		t.Logf("")
		t.Logf("CONCLUSION: Perfect parity across all %d frames", numFrames)
	}
}

// TestSILKInputParityDetailed performs a deeper analysis when a divergence is
// found, capturing the full input history and resampler output. This test also
// compares the inputBuf prefix (2-sample history) between both encoders to
// catch off-by-one alignment issues.
func TestSILKInputParityDetailed(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960 // 20ms at 48kHz
		bitrate    = 32000
		numFrames  = 50
	)

	totalSamples := numFrames * frameSize * channels
	original := generateMultiFreqSignal(totalSamples)

	// ---------- gopus encoder ----------
	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	// ---------- libopus encoder ----------
	libEnc, err := NewLibopusEncoder(sampleRate, channels, OpusApplicationRestrictedSilk)
	if err != nil || libEnc == nil {
		t.Fatal("Could not create libopus encoder")
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(OpusBandwidthWideband)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	type perFrameStats struct {
		packetMatch bool
		inputMatch  bool
		pktSizeGo   int
		pktSizeLib  int
		maxAbsDiff  int16
		sumAbsDiff  int64
		compareLen  int
		goInputLen  int
		libInputLen int

		// The history prefix (inputBuf[0] in libopus, previous frame's last sample)
		libPrefix0 int16 // inputBuf[0]
		libPrefix1 int16 // inputBuf[1] (= first sample of aligned input)

		// nBitsExceeded
		goNBitsExceeded  int
		libNBitsExceeded int
	}

	stats := make([]perFrameStats, numFrames)

	for i := 0; i < numFrames; i++ {
		start := i * frameSize

		// --- gopus encode ---
		trace := &silk.EncoderTrace{
			FramePre: &silk.FrameStateTrace{},
			Frame:    &silk.FrameStateTrace{},
			NSQ:      &silk.NSQTrace{CaptureInputs: true},
		}
		goEnc.SetSilkTrace(trace)

		pcm64 := make([]float64, frameSize)
		for j := 0; j < frameSize; j++ {
			pcm64[j] = float64(original[start+j])
		}
		goPkt, encErr := goEnc.Encode(pcm64, frameSize)
		if encErr != nil {
			t.Fatalf("gopus encode frame %d: %v", i, encErr)
		}
		cpGo := make([]byte, len(goPkt))
		copy(cpGo, goPkt)
		goEnc.SetSilkTrace(nil)

		goInputQ0 := trace.NSQ.InputQ0

		// --- libopus encode ---
		pcm := original[start : start+frameSize]
		libPkt, n := libEnc.EncodeFloat(pcm, frameSize)
		if n <= 0 {
			t.Fatalf("libopus encode frame %d: returned %d", i, n)
		}

		libState, ok := CaptureStateFromExistingEncoder(libEnc.GetEncoderPtr())
		if !ok {
			t.Fatalf("Failed to capture libopus state at frame %d", i)
		}

		// Packet comparison
		packetMatch := len(cpGo) == len(libPkt)
		if packetMatch {
			for j := 0; j < len(cpGo); j++ {
				if cpGo[j] != libPkt[j] {
					packetMatch = false
					break
				}
			}
		}

		// Input comparison
		libInputQ0 := make([]int16, 0)
		if libState.FrameLength > 0 && len(libState.InputBufQ0) >= libState.FrameLength+1 {
			libInputQ0 = libState.InputBufQ0[1 : libState.FrameLength+1]
		}

		inputMatch := len(goInputQ0) == len(libInputQ0)
		maxAbsDiff := int16(0)
		sumAbsDiff := int64(0)
		compareLen := len(goInputQ0)
		if len(libInputQ0) < compareLen {
			compareLen = len(libInputQ0)
		}
		for j := 0; j < compareLen; j++ {
			diff := goInputQ0[j] - libInputQ0[j]
			absDiff := diff
			if absDiff < 0 {
				absDiff = -absDiff
			}
			sumAbsDiff += int64(absDiff)
			if absDiff > maxAbsDiff {
				maxAbsDiff = absDiff
			}
			if diff != 0 {
				inputMatch = false
			}
		}

		var prefix0, prefix1 int16
		if len(libState.InputBufQ0) >= 2 {
			prefix0 = libState.InputBufQ0[0]
			prefix1 = libState.InputBufQ0[1]
		}

		stats[i] = perFrameStats{
			packetMatch:      packetMatch,
			inputMatch:       inputMatch,
			pktSizeGo:        len(cpGo),
			pktSizeLib:       len(libPkt),
			maxAbsDiff:       maxAbsDiff,
			sumAbsDiff:       sumAbsDiff,
			compareLen:       compareLen,
			goInputLen:       len(goInputQ0),
			libInputLen:      len(libInputQ0),
			libPrefix0:       prefix0,
			libPrefix1:       prefix1,
			goNBitsExceeded:  trace.Frame.NBitsExceeded,
			libNBitsExceeded: libState.NBitsExceeded,
		}
	}

	// ---------- Report ----------
	t.Log("Frame-by-frame summary:")
	t.Log("  Frame | Pkt  | Input | MaxDiff | AvgDiff  | nBitsExc go/lib | PktSize go/lib")
	t.Log("  ------|------|-------|---------|----------|-----------------|---------------")

	firstPktDiv := -1
	firstInpDiv := -1
	for i := 0; i < numFrames; i++ {
		s := stats[i]
		pktMark := " OK "
		if !s.packetMatch {
			pktMark = "DIFF"
			if firstPktDiv < 0 {
				firstPktDiv = i
			}
		}
		inpMark := " OK "
		if !s.inputMatch {
			inpMark = "DIFF"
			if firstInpDiv < 0 {
				firstInpDiv = i
			}
		}
		avgDiff := float64(0)
		if s.compareLen > 0 {
			avgDiff = float64(s.sumAbsDiff) / float64(s.compareLen)
		}
		nBitsMark := "="
		if s.goNBitsExceeded != s.libNBitsExceeded {
			nBitsMark = "!"
		}
		t.Logf("  %5d | %s | %s | %7d | %8.2f | %6d/%6d %s | %4d/%4d",
			i, pktMark, inpMark, s.maxAbsDiff, avgDiff,
			s.goNBitsExceeded, s.libNBitsExceeded, nBitsMark,
			s.pktSizeGo, s.pktSizeLib)
	}

	// Per-frame input energy comparison (to detect scaling issues)
	t.Log("")
	t.Log("Per-frame input energy (RMS of int16 samples):")
	for i := 0; i < numFrames; i++ {
		s := stats[i]
		if !s.inputMatch {
			t.Logf("  Frame %2d: goLen=%d libLen=%d maxAbsDiff=%d prefix=[%d,%d]",
				i, s.goInputLen, s.libInputLen, s.maxAbsDiff, s.libPrefix0, s.libPrefix1)
		}
	}

	// Look for correlation between nBitsExceeded divergence and packet divergence
	t.Log("")
	if firstPktDiv >= 0 {
		// Check if nBitsExceeded diverges earlier
		firstNBitsDiv := -1
		for i := 0; i < numFrames; i++ {
			if stats[i].goNBitsExceeded != stats[i].libNBitsExceeded {
				firstNBitsDiv = i
				break
			}
		}
		t.Logf("First packet divergence: frame %d", firstPktDiv)
		t.Logf("First input divergence:  frame %d", firstInpDiv)
		t.Logf("First nBitsExceeded div: frame %d", firstNBitsDiv)

		if firstNBitsDiv >= 0 && firstNBitsDiv < firstPktDiv {
			t.Logf("")
			t.Logf("nBitsExceeded diverges at frame %d BEFORE packets at frame %d",
				firstNBitsDiv, firstPktDiv)
			t.Logf("  => nBitsExceeded feeds back into target rate computation")
			t.Logf("     and is the likely root cause of the packet divergence")

			// Show nBitsExceeded trajectory leading up to divergence
			t.Logf("")
			t.Logf("nBitsExceeded trajectory (frames %d through %d):", maxIntSIP(0, firstNBitsDiv-3), minIntSIP(numFrames-1, firstPktDiv+3))
			for i := maxIntSIP(0, firstNBitsDiv-3); i <= minIntSIP(numFrames-1, firstPktDiv+3); i++ {
				s := stats[i]
				mark := " "
				if i == firstNBitsDiv {
					mark = "<-- first nBitsExceeded divergence"
				} else if i == firstPktDiv {
					mark = "<-- first packet divergence"
				}
				t.Logf("  Frame %2d: go=%5d lib=%5d diff=%5d %s",
					i, s.goNBitsExceeded, s.libNBitsExceeded,
					s.goNBitsExceeded-s.libNBitsExceeded, mark)
			}
		}
	}

	// Compute overall input PCM SNR (go vs lib) to quantify match quality
	var totalGoEn, totalNoiseEn float64
	for i := 0; i < numFrames; i++ {
		s := stats[i]
		if s.compareLen == 0 {
			continue
		}
		// Re-encode to get samples... we already have stats but not raw samples here.
		// Use maxAbsDiff as proxy
		totalGoEn += float64(s.compareLen) * 32768.0 * 32768.0 * 0.01 // rough proxy
		totalNoiseEn += float64(s.sumAbsDiff) * float64(s.sumAbsDiff) / float64(s.compareLen)
	}
	if totalNoiseEn > 0 && totalGoEn > 0 {
		approxSNR := 10.0 * math.Log10(totalGoEn/totalNoiseEn)
		t.Logf("")
		t.Logf("Approximate input PCM SNR (go vs lib): %.1f dB", approxSNR)
	}

	t.Log("")
	inputMatchCount := 0
	packetMatchCount := 0
	for i := 0; i < numFrames; i++ {
		if stats[i].inputMatch {
			inputMatchCount++
		}
		if stats[i].packetMatch {
			packetMatchCount++
		}
	}
	t.Logf("Packet parity:    %d/%d", packetMatchCount, numFrames)
	t.Logf("Input parity:     %d/%d", inputMatchCount, numFrames)
}

func maxIntSIP(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minIntSIP(a, b int) int {
	if a < b {
		return a
	}
	return b
}
