//go:build ignore
// +build ignore

// Package cgo provides CGO comparison tests for CELT decoding.
// This file contains detailed debugging for packet 31 from testvector07.
//
// NOTE: This file requires CGO and can only be run when the mdct_libopus_test.go
// CGO issue is resolved. Use internal/testvectors/packet31_debug_test.go instead.
package cgo

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Packet31DebugTrace holds traced values from the decode pipeline
type Packet31DebugTrace struct {
	// Frame header
	FrameSize   int
	Channels    int
	LM          int
	Intra       bool
	Transient   bool
	ShortBlocks int

	// Postfilter
	PostfilterPeriod int
	PostfilterGain   float64
	PostfilterTapset int

	// Energy values
	CoarseEnergies []float64 // Per-band coarse energy
	FineEnergies   []float64 // After fine energy decode
	FinalEnergies  []float64 // After energy finalise

	// TF decode
	TFRes []int

	// Allocation
	Pulses       []int
	FineQuant    []int
	FinePriority []int

	// PVQ coefficients per band (normalized)
	PVQCoeffs [][]float64

	// Pre-synthesis frequency domain
	FreqDomainL []float64
	FreqDomainR []float64

	// Post-IMDCT time domain (before overlap-add)
	PostIMDCT []float64

	// Post-overlap time domain
	PostOverlap []float64

	// Final PCM output
	FinalPCM []float64
}

// TestPacket31DetailedComparison decodes packet 31 with detailed tracing
func TestPacket31DetailedComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	channels := 2

	if targetPacket >= len(packets) {
		t.Fatalf("Packet %d not available (only %d packets)", targetPacket, len(packets))
	}

	// Create fresh decoders and process all packets up to target
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode packets 0 to targetPacket-1 to establish state
	for i := 0; i < targetPacket; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])

	t.Logf("=== Packet %d Analysis ===", targetPacket)
	t.Logf("Packet length: %d bytes", len(pkt))
	t.Logf("TOC: stereo=%v, frameSize=%d, mode=%v", toc.Stereo, toc.FrameSize, toc.Mode)
	t.Logf("First 16 bytes: %x", pkt[:minInt(16, len(pkt))])

	// Decode with both implementations
	goPcm, goErr := goDec.DecodeFloat32(pkt)
	if goErr != nil {
		t.Fatalf("gopus decode failed: %v", goErr)
	}

	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	goSamples := len(goPcm) / channels
	t.Logf("gopus samples: %d, libopus samples: %d", goSamples, libSamples)

	// Find first divergence point
	findDivergencePoint(t, goPcm, libPcm, channels)

	// Calculate SNR
	var sigPow, noisePow float64
	for i := 0; i < goSamples*channels; i++ {
		sig := float64(libPcm[i])
		noise := float64(goPcm[i]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}
	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("SNR: %.1f dB", snr)
}

// findDivergencePoint locates the exact sample where divergence begins
func findDivergencePoint(t *testing.T, goPcm, libPcm []float32, channels int) {
	const threshold = 0.001
	goSamples := len(goPcm) / channels

	firstDiffIdx := -1
	var maxDiff float64
	maxDiffIdx := 0

	for i := 0; i < goSamples*channels && i < len(libPcm); i++ {
		diff := math.Abs(float64(goPcm[i]) - float64(libPcm[i]))
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
		if diff > threshold && firstDiffIdx == -1 {
			firstDiffIdx = i
		}
	}

	t.Logf("\n=== Divergence Analysis ===")
	t.Logf("First significant diff (>%.4f) at index %d", threshold, firstDiffIdx)
	t.Logf("Max diff: %.6f at index %d (sample %d, ch %d)",
		maxDiff, maxDiffIdx, maxDiffIdx/channels, maxDiffIdx%channels)

	if firstDiffIdx >= 0 {
		t.Logf("\n--- Samples around first divergence (index %d) ---", firstDiffIdx)
		t.Logf("Idx\t\tgopus\t\tlibopus\t\tdiff")
		start := maxInt(0, firstDiffIdx-5)
		end := minInt(len(goPcm), firstDiffIdx+20)
		for i := start; i < end && i < len(libPcm); i++ {
			diff := goPcm[i] - libPcm[i]
			marker := ""
			if i == firstDiffIdx {
				marker = " <-- FIRST DIVERGENCE"
			}
			t.Logf("%d\t\t%.6f\t%.6f\t%.6f%s", i, goPcm[i], libPcm[i], diff, marker)
		}
	}
}

// TestPacket31BitExactDecode performs detailed bit-level decode comparison
func TestPacket31BitExactDecode(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	if targetPacket >= len(packets) {
		t.Fatalf("Packet %d not available", targetPacket)
	}

	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])

	t.Logf("=== Packet %d Bit-Exact Analysis ===", targetPacket)
	t.Logf("TOC: stereo=%v, frameSize=%d, mode=%v", toc.Stereo, toc.FrameSize, toc.Mode)

	// Extract CELT frame data (skip TOC byte)
	frameData := pkt[1:]

	// Initialize range decoder and trace state
	rd := &rangecoding.Decoder{}
	rd.Init(frameData)

	t.Logf("\n--- Range Decoder Initial State ---")
	t.Logf("Range: %d, Val: %d, Storage bits: %d", rd.Range(), rd.Val(), rd.StorageBits())

	// Get mode config
	mode := celt.GetModeConfig(toc.FrameSize)
	lm := mode.LM
	end := celt.EffectiveBandsForFrameSize(celt.CELTFullband, toc.FrameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}

	t.Logf("\n--- Frame Parameters ---")
	t.Logf("LM: %d, EffBands: %d", lm, end)

	// Decode header flags
	totalBits := len(frameData) * 8
	tell := rd.Tell()

	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	t.Logf("Silence: %v (tell=%d, totalBits=%d)", silence, tell, totalBits)

	if silence {
		t.Logf("Silence frame - no further decoding")
		return
	}

	// Postfilter (mono, so start=0)
	start := 0
	postfilterPeriod := 0
	postfilterGain := 0.0
	postfilterTapset := 0
	tell = rd.Tell()
	if start == 0 && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				postfilterTapset = rd.DecodeICDF([]byte{18, 35, 64, 64}, 2)
			}
			postfilterGain = 0.09375 * float64(qg+1)
		}
		tell = rd.Tell()
	}
	t.Logf("\n--- Postfilter ---")
	t.Logf("Period: %d, Gain: %.4f, Tapset: %d", postfilterPeriod, postfilterGain, postfilterTapset)
	t.Logf("Tell after postfilter: %d", tell)

	// Transient flag
	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	t.Logf("Transient: %v (tell=%d)", transient, tell)

	// Intra flag
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}
	t.Logf("Intra: %v (tell=%d)", intra, rd.Tell())

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}
	t.Logf("ShortBlocks: %d", shortBlocks)

	// Note: full energy and band decoding would require creating a full CELT decoder
	// For now, we've traced the header decoding which is often where issues arise
	t.Logf("\n--- Summary ---")
	t.Logf("This is a %s frame with frameSize=%d, %d bands",
		map[bool]string{true: "transient", false: "non-transient"}[transient],
		toc.FrameSize, end)
}

// TestPacket31EnergyTrace traces energy decoding for packet 31
func TestPacket31EnergyTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	channels := 2

	if targetPacket >= len(packets) {
		t.Fatalf("Packet %d not available", targetPacket)
	}

	// Create CELT decoder for detailed tracing
	celtDec := celt.NewDecoder(channels)

	// Process packets 0..30 to build up state
	for i := 0; i < targetPacket; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		frameData := pkt[1:]

		// Skip non-CELT packets
		if toc.Mode != gopus.ModeCELT {
			continue
		}

		if toc.Stereo {
			celtDec.DecodeFrame(frameData, toc.FrameSize)
		} else {
			celtDec.DecodeFrameWithPacketStereo(frameData, toc.FrameSize, false)
		}
	}

	// Now decode packet 31 with tracing
	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("=== Packet %d Energy Trace ===", targetPacket)
	t.Logf("TOC: stereo=%v, frameSize=%d", toc.Stereo, toc.FrameSize)

	// Get previous energies before decode
	prevEnergies := make([]float64, len(celtDec.PrevEnergy()))
	copy(prevEnergies, celtDec.PrevEnergy())

	t.Logf("\n--- Previous Frame Energies (first 10 bands per channel) ---")
	mode := celt.GetModeConfig(toc.FrameSize)
	nbBands := mode.EffBands
	for c := 0; c < channels; c++ {
		t.Logf("Channel %d:", c)
		for band := 0; band < minInt(10, nbBands); band++ {
			idx := c*celt.MaxBands + band
			if idx < len(prevEnergies) {
				t.Logf("  Band %2d: %.4f", band, prevEnergies[idx])
			}
		}
	}

	// Decode the packet
	frameData := pkt[1:]
	var pcm []float64
	var err error

	if toc.Stereo {
		pcm, err = celtDec.DecodeFrame(frameData, toc.FrameSize)
	} else {
		pcm, err = celtDec.DecodeFrameWithPacketStereo(frameData, toc.FrameSize, false)
	}

	if err != nil {
		t.Fatalf("CELT decode failed: %v", err)
	}

	// Get new energies after decode
	newEnergies := celtDec.PrevEnergy()

	t.Logf("\n--- New Frame Energies (first 10 bands per channel) ---")
	for c := 0; c < channels; c++ {
		t.Logf("Channel %d:", c)
		for band := 0; band < minInt(10, nbBands); band++ {
			idx := c*celt.MaxBands + band
			if idx < len(newEnergies) {
				t.Logf("  Band %2d: %.4f", band, newEnergies[idx])
			}
		}
	}

	// Log first few output samples
	t.Logf("\n--- First 20 output samples (interleaved stereo) ---")
	for i := 0; i < minInt(20, len(pcm)); i++ {
		t.Logf("  [%2d] %.6f", i, pcm[i])
	}
}

// TestPacket31FullPipelineTrace traces the complete decode pipeline
func TestPacket31FullPipelineTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	channels := 2

	// Create both decoders fresh
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	t.Logf("=== Full Pipeline Trace for Packet %d ===", targetPacket)

	// Process packets, logging SNR for each
	for i := 0; i <= targetPacket && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, goErr := goDec.DecodeFloat32(pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		if goErr != nil || libSamples < 0 {
			t.Logf("Pkt %d: decode error (go=%v, lib=%d)", i, goErr, libSamples)
			continue
		}

		goSamples := len(goPcm) / channels
		if goSamples != libSamples {
			t.Logf("Pkt %d: sample count mismatch (%d vs %d)", i, goSamples, libSamples)
			continue
		}

		var sigPow, noisePow float64
		var maxDiff float64
		firstDiffIdx := -1
		const threshold = 0.001

		for j := 0; j < goSamples*channels; j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			diff := math.Abs(noise)
			if diff > maxDiff {
				maxDiff = diff
			}
			if diff > threshold && firstDiffIdx == -1 {
				firstDiffIdx = j
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		marker := ""
		if snr < 40 {
			marker = " *** LOW SNR ***"
		}
		if i == targetPacket {
			marker += " <-- TARGET"
		}

		t.Logf("Pkt %3d: SNR=%6.1f dB, maxDiff=%.6f, firstDiv@%d, stereo=%v, frame=%d%s",
			i, snr, maxDiff, firstDiffIdx, toc.Stereo, toc.FrameSize, marker)
	}
}

// TestPacket31SampleBysSampleDivergence provides sample-by-sample comparison
func TestPacket31SampleBySampleDivergence(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	channels := 2

	// Create decoders and warm up with prior packets
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	for i := 0; i < targetPacket; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])

	goPcm, _ := goDec.DecodeFloat32(pkt)
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

	t.Logf("=== Sample-by-Sample Analysis for Packet %d ===", targetPacket)
	t.Logf("Frame size: %d, Stereo: %v", toc.FrameSize, toc.Stereo)

	goSamples := len(goPcm) / channels

	// Find the divergence region
	const threshold = 0.0001
	firstDiv := -1
	lastDiv := -1

	for i := 0; i < goSamples*channels && i < len(libPcm); i++ {
		diff := math.Abs(float64(goPcm[i]) - float64(libPcm[i]))
		if diff > threshold {
			if firstDiv == -1 {
				firstDiv = i
			}
			lastDiv = i
		}
	}

	t.Logf("Divergence range: [%d, %d] (samples)", firstDiv, lastDiv)

	if firstDiv >= 0 {
		// Convert to sample index (accounting for stereo interleaving)
		firstSample := firstDiv / channels
		t.Logf("First diverging sample: %d (index in buffer: %d)", firstSample, firstDiv)

		// Show detailed L/R comparison around divergence
		t.Logf("\n--- L/R Comparison around divergence (sample %d corresponds to index %d) ---", firstSample, firstDiv)
		t.Logf("Sample\tL_go\t\tL_lib\t\tL_diff\t\tR_go\t\tR_lib\t\tR_diff")

		startSample := maxInt(0, firstSample-3)
		endSample := minInt(libSamples, firstSample+20)

		for s := startSample; s < endSample; s++ {
			lIdx := s * 2
			rIdx := s*2 + 1

			var lGo, lLib, rGo, rLib float32
			if lIdx < len(goPcm) {
				lGo = goPcm[lIdx]
			}
			if lIdx < len(libPcm) {
				lLib = libPcm[lIdx]
			}
			if rIdx < len(goPcm) {
				rGo = goPcm[rIdx]
			}
			if rIdx < len(libPcm) {
				rLib = libPcm[rIdx]
			}

			lDiff := lGo - lLib
			rDiff := rGo - rLib

			marker := ""
			if s == firstSample {
				marker = " <-- FIRST DIVERGENCE"
			}

			t.Logf("%d\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f%s",
				s, lGo, lLib, lDiff, rGo, rLib, rDiff, marker)
		}
	}

	// Calculate SNR
	var sigPow, noisePow float64
	for i := 0; i < goSamples*channels && i < len(libPcm); i++ {
		sig := float64(libPcm[i])
		noise := float64(goPcm[i]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}
	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("\nPacket %d total SNR: %.1f dB", targetPacket, snr)
}

// TestPacket31ChannelAnalysis analyzes each channel separately
func TestPacket31ChannelAnalysis(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	channels := 2

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	for i := 0; i < targetPacket; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])

	goPcm, _ := goDec.DecodeFloat32(pkt)
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

	t.Logf("=== Per-Channel Analysis for Packet %d ===", targetPacket)
	t.Logf("Packet is %s, frame size %d", map[bool]string{true: "STEREO", false: "MONO"}[toc.Stereo], toc.FrameSize)

	// Separate channels
	goL := make([]float32, libSamples)
	goR := make([]float32, libSamples)
	libL := make([]float32, libSamples)
	libR := make([]float32, libSamples)

	for i := 0; i < libSamples; i++ {
		if i*2 < len(goPcm) {
			goL[i] = goPcm[i*2]
		}
		if i*2+1 < len(goPcm) {
			goR[i] = goPcm[i*2+1]
		}
		libL[i] = libPcm[i*2]
		libR[i] = libPcm[i*2+1]
	}

	// Calculate per-channel SNR
	calcChannelSNR := func(go_, lib []float32, name string) {
		var sigPow, noisePow, maxDiff float64
		maxDiffIdx := 0
		firstDiffIdx := -1
		const threshold = 0.001

		for i := 0; i < len(go_) && i < len(lib); i++ {
			sig := float64(lib[i])
			noise := float64(go_[i]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			diff := math.Abs(noise)
			if diff > maxDiff {
				maxDiff = diff
				maxDiffIdx = i
			}
			if diff > threshold && firstDiffIdx == -1 {
				firstDiffIdx = i
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		t.Logf("\n%s channel:", name)
		t.Logf("  SNR: %.1f dB", snr)
		t.Logf("  Max diff: %.6f at sample %d", maxDiff, maxDiffIdx)
		t.Logf("  First divergence (>%.4f): sample %d", threshold, firstDiffIdx)
	}

	calcChannelSNR(goL, libL, "Left")
	calcChannelSNR(goR, libR, "Right")

	// Check if L and R are identical (mono duplicated to stereo)
	identicalInLib := true
	identicalInGo := true
	for i := 0; i < libSamples; i++ {
		if libL[i] != libR[i] {
			identicalInLib = false
		}
		if goL[i] != goR[i] {
			identicalInGo = false
		}
	}

	t.Logf("\n--- Channel Identity Check ---")
	t.Logf("libopus L==R: %v (mono packet duplicated to stereo)", identicalInLib)
	t.Logf("gopus L==R: %v", identicalInGo)
}

// TestPacket31FrequencyDomainAnalysis analyzes frequency content differences
func TestPacket31FrequencyDomainAnalysis(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	channels := 2

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	for i := 0; i < targetPacket; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	pkt := packets[targetPacket]
	goPcm, _ := goDec.DecodeFloat32(pkt)
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

	t.Logf("=== Frequency Domain Analysis for Packet %d ===", targetPacket)

	// Calculate error magnitude in different regions of the output
	// This helps identify if error is concentrated in low/mid/high frequencies
	// or in the overlap region (first 120 samples)

	goSamples := len(goPcm) / channels

	// Analyze mono channel (or left for stereo)
	goMono := make([]float64, goSamples)
	libMono := make([]float64, libSamples)

	for i := 0; i < goSamples && i < libSamples; i++ {
		goMono[i] = float64(goPcm[i*2]) // Left channel
		libMono[i] = float64(libPcm[i*2])
	}

	// Analyze error in regions
	analyzeRegion := func(start, end int, name string) {
		if end > len(goMono) {
			end = len(goMono)
		}
		if end > len(libMono) {
			end = len(libMono)
		}
		if start >= end {
			return
		}

		var sigPow, noisePow, maxDiff float64
		for i := start; i < end; i++ {
			sig := libMono[i]
			noise := goMono[i] - sig
			sigPow += sig * sig
			noisePow += noise * noise
			diff := math.Abs(noise)
			if diff > maxDiff {
				maxDiff = diff
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		t.Logf("%s [%d:%d]: SNR=%.1f dB, maxDiff=%.6f", name, start, end, snr, maxDiff)
	}

	// Overlap region (first 120 samples - CELT overlap)
	overlap := 120
	analyzeRegion(0, overlap, "Overlap region")

	// Post-overlap (samples 120-480 for 960-sample frame)
	analyzeRegion(overlap, goSamples/2, "First half (post-overlap)")

	// Second half
	analyzeRegion(goSamples/2, goSamples, "Second half")

	// Small windows to localize divergence
	windowSize := 64
	t.Logf("\n--- Error by %d-sample windows ---", windowSize)
	for start := 0; start < minInt(goSamples, libSamples); start += windowSize {
		end := start + windowSize
		if end > minInt(goSamples, libSamples) {
			end = minInt(goSamples, libSamples)
		}

		var sigPow, noisePow float64
		for i := start; i < end; i++ {
			sig := libMono[i]
			noise := goMono[i] - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		if snr < 60 {
			t.Logf("  [%4d:%4d] SNR=%.1f dB ***", start, end, snr)
		}
	}
}

// TestPacket31StateCompare compares decoder state before and after packet 31
func TestPacket31StateCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	targetPacket := 31
	channels := 2

	t.Logf("=== Decoder State Analysis for Packet %d ===", targetPacket)

	// Create CELT decoder for state inspection
	celtDec := celt.NewDecoder(channels)

	// Process packets 0..29 and capture state before packets 30 and 31
	for i := 0; i < targetPacket; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		frameData := pkt[1:]

		if toc.Mode.String() != "CELT" {
			continue
		}

		if i == targetPacket-1 {
			// Log state before packet 31 (after packet 30)
			t.Logf("\n--- State after packet %d (before packet %d) ---", i, targetPacket)
			t.Logf("Overlap buffer (first 10): %v", celtDec.OverlapBuffer()[:minInt(10, len(celtDec.OverlapBuffer()))])
			t.Logf("Preemph state: %v", celtDec.PreemphState())
			t.Logf("RNG: %d", celtDec.RNG())
			t.Logf("Postfilter period: %d, gain: %.4f, tapset: %d",
				celtDec.PostfilterPeriod(), celtDec.PostfilterGain(), celtDec.PostfilterTapset())
		}

		if toc.Stereo {
			celtDec.DecodeFrame(frameData, toc.FrameSize)
		} else {
			celtDec.DecodeFrameWithPacketStereo(frameData, toc.FrameSize, false)
		}
	}

	// Log packet 31 details
	pkt := packets[targetPacket]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("\n--- Packet %d ---", targetPacket)
	t.Logf("TOC: stereo=%v, frameSize=%d, mode=%v", toc.Stereo, toc.FrameSize, toc.Mode)
	t.Logf("Packet bytes (hex): %x", pkt[:minInt(32, len(pkt))])
}

// Helper to load packets using the existing pattern
func loadPacketsFromBitFile(filename string, maxPackets int) ([][]byte, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		if maxPackets > 0 && len(packets) >= maxPackets {
			break
		}
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:]) // finalRange
		offset += 4

		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}

		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	return packets, nil
}

// TestPacket31CumulativeError tracks error accumulation leading to packet 31
func TestPacket31CumulativeError(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 2

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	t.Logf("=== Cumulative Error Analysis (packets 0-35) ===")
	t.Logf("Pkt\tStereo\tFrame\tSNR(dB)\tMaxDiff\t\tFirstDiv\tNote")

	for i := 0; i <= minInt(35, len(packets)-1); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, goErr := goDec.DecodeFloat32(pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		if goErr != nil || libSamples < 0 {
			t.Logf("%d\t%v\t%d\tERROR", i, toc.Stereo, toc.FrameSize)
			continue
		}

		goSamples := len(goPcm) / channels
		if goSamples != libSamples {
			t.Logf("%d\t%v\t%d\tSAMPLE COUNT MISMATCH", i, toc.Stereo, toc.FrameSize)
			continue
		}

		var sigPow, noisePow, maxDiff float64
		firstDiffIdx := -1
		const threshold = 0.001

		for j := 0; j < goSamples*channels; j++ {
			sig := float64(libPcm[j])
			noise := float64(goPcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			diff := math.Abs(noise)
			if diff > maxDiff {
				maxDiff = diff
			}
			if diff > threshold && firstDiffIdx == -1 {
				firstDiffIdx = j
			}
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		note := ""
		if snr < 10 {
			note = "*** VERY LOW ***"
		} else if snr < 40 {
			note = "* LOW *"
		}

		stereoStr := map[bool]string{true: "Y", false: "N"}[toc.Stereo]
		t.Logf("%d\t%s\t%d\t%.1f\t%.6f\t%d\t\t%s",
			i, stereoStr, toc.FrameSize, snr, maxDiff, firstDiffIdx, note)
	}
}

func boolToIntLocal(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Ensure minInt and maxInt are available (may be defined elsewhere)
func minIntLocal(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxIntLocal(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Utility: format bytes as hex for logging
func formatHex(data []byte, max int) string {
	if len(data) > max {
		data = data[:max]
	}
	return fmt.Sprintf("%x", data)
}
