//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK encoding quality.
package cgo

import (
	"fmt"
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// generateMultiFreqSignal generates a multi-frequency test signal with amplitude modulation.
func generateMultiFreqSignal(samples int) []float32 {
	signal := make([]float32, samples)
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	modFreqs := []float64{1.3, 2.7, 0.9}

	for i := 0; i < samples; i++ {
		t := float64(i) / 48000.0
		var val float64
		for fi, freq := range freqs {
			modDepth := 0.5 + 0.5*math.Sin(2*math.Pi*modFreqs[fi]*t)
			val += amp * modDepth * math.Sin(2*math.Pi*freq*t)
		}
		// Onset ramp
		onsetSamples := int(0.010 * 48000)
		if i < onsetSamples {
			frac := float64(i) / float64(onsetSamples)
			val *= frac * frac * frac
		}
		signal[i] = float32(val)
	}
	return signal
}

// TestSILKEncoderStateCompare encodes frame-by-frame with both gopus and libopus,
// capturing internal encoder state after each frame to identify the first divergence.
func TestSILKEncoderStateCompare(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960 // 20ms at 48kHz
		bitrate    = 32000
		numFrames  = 50
	)

	totalSamples := numFrames * frameSize * channels
	original := generateMultiFreqSignal(totalSamples)

	// --- gopus encoder with trace hooks ---
	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	// --- libopus encoder (persistent instance for state capture) ---
	libEnc, err := NewLibopusEncoder(sampleRate, channels, OpusApplicationRestrictedSilk)
	if err != nil || libEnc == nil {
		t.Fatal("Could not create libopus encoder")
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(OpusBandwidthWideband)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	goPackets := make([][]byte, numFrames)
	libPackets := make([][]byte, numFrames)

	type frameState struct {
		goNBitsExceeded   int
		libNBitsExceeded  int
		goTargetRate      int
		libTargetRate     int
		goSNRDBQ7         int
		libSNRDBQ7        int
		goSumLogGainQ7    int32
		libSumLogGainQ7   int32
		goLastGainIdx     int
		libLastGainIdx    int
		goSignalType      int
		libSignalType     int
		goPrevLag         int
		libPrevLag        int
		goNSQRandSeed     int32
		libNSQRandSeed    int32
		goNSQPrevGainQ16  int32
		libNSQPrevGainQ16 int32
		goSpeechQ8        int
		libSpeechQ8       int
		goInputTiltQ15    int
		libInputTiltQ15   int
		goNSQXQHash       uint64
		libNSQXQHash      uint64
		goPitchBufHash    uint64
		libPitchBufHash   uint64
		goPitchWinHash    uint64
		libPitchWinHash   uint64
	}

	states := make([]frameState, numFrames)

	t.Logf("Encoding %d frames, capturing state after each frame...", numFrames)

	for i := 0; i < numFrames; i++ {
		start := i * frameSize

		// --- Encode with gopus, capturing trace ---
		trace := &silk.EncoderTrace{
			FramePre: &silk.FrameStateTrace{},
			Frame:    &silk.FrameStateTrace{},
		}
		goEnc.SetSilkTrace(trace)

		pcm64 := make([]float64, frameSize)
		for j := 0; j < frameSize; j++ {
			pcm64[j] = float64(original[start+j])
		}
		packet, encErr := goEnc.Encode(pcm64, frameSize)
		if encErr != nil {
			t.Fatalf("gopus encode frame %d: %v", i, encErr)
		}
		cp := make([]byte, len(packet))
		copy(cp, packet)
		goPackets[i] = cp
		goEnc.SetSilkTrace(nil)

		// Capture gopus state from trace (Frame = after encode)
		goFrame := trace.Frame
		goPre := trace.FramePre

		// --- Encode with libopus ---
		pcm := original[start : start+frameSize]
		pkt, n := libEnc.EncodeFloat(pcm, frameSize)
		if n <= 0 {
			t.Fatalf("libopus encode frame %d: returned %d", i, n)
		}
		libPackets[i] = pkt

		// Capture libopus state from existing encoder (after encode)
		libState, ok := CaptureStateFromExistingEncoder(libEnc.GetEncoderPtr())
		if !ok {
			t.Fatalf("Failed to capture libopus state at frame %d", i)
		}

		// Store state for comparison
		// FramePre captures state BEFORE the frame encodes (includes nBitsExceeded from prior)
		// Frame captures state AFTER the frame encodes
		states[i] = frameState{
			goNBitsExceeded:   goFrame.NBitsExceeded,
			libNBitsExceeded:  libState.NBitsExceeded,
			goTargetRate:      goPre.TargetRateBps,
			libTargetRate:     libState.TargetRateBps,
			goSNRDBQ7:         goPre.SNRDBQ7,
			libSNRDBQ7:        libState.SNRDBQ7,
			goSumLogGainQ7:    goFrame.SumLogGainQ7,
			libSumLogGainQ7:   libState.SumLogGainQ7,
			goLastGainIdx:     int(goFrame.LastGainIndex),
			libLastGainIdx:    libState.LastGainIndex,
			goSignalType:      goFrame.SignalType,
			libSignalType:     libState.SignalType,
			goPrevLag:         goFrame.PrevLag,
			libPrevLag:        libState.PrevLag,
			goNSQRandSeed:     goFrame.NSQRandSeed,
			libNSQRandSeed:    libState.NSQRandSeed,
			goNSQPrevGainQ16:  goFrame.NSQPrevGainQ16,
			libNSQPrevGainQ16: libState.NSQPrevGainQ16,
			goSpeechQ8:        goFrame.SpeechActivityQ8,
			libSpeechQ8:       libState.SpeechActivityQ8,
			goInputTiltQ15:    goFrame.InputTiltQ15,
			libInputTiltQ15:   libState.InputTiltQ15,
			goNSQXQHash:       goFrame.NSQXQHash,
			libNSQXQHash:      libState.NSQXQHash,
			goPitchBufHash:    goFrame.PitchBufHash,
			libPitchBufHash:   libState.PitchXBufHash,
			goPitchWinHash:    goFrame.PitchWinHash,
			libPitchWinHash:   libState.PitchWinHash,
		}
	}

	// --- Log state comparison ---
	firstDivergence := -1
	for i := 0; i < numFrames; i++ {
		s := states[i]
		goLen := len(goPackets[i])
		libLen := len(libPackets[i])
		packetMatch := goLen == libLen
		if packetMatch {
			for j := 0; j < goLen; j++ {
				if goPackets[i][j] != libPackets[i][j] {
					packetMatch = false
					break
				}
			}
		}

		// Check for any state divergence
		diverged := false
		var diffs []string

		if s.goNBitsExceeded != s.libNBitsExceeded {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("nBitsExceeded: go=%d lib=%d", s.goNBitsExceeded, s.libNBitsExceeded))
		}
		if s.goTargetRate != s.libTargetRate {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("targetRate: go=%d lib=%d", s.goTargetRate, s.libTargetRate))
		}
		if s.goSNRDBQ7 != s.libSNRDBQ7 {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("SNRDBQ7: go=%d lib=%d", s.goSNRDBQ7, s.libSNRDBQ7))
		}
		if s.goSumLogGainQ7 != s.libSumLogGainQ7 {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("sumLogGainQ7: go=%d lib=%d", s.goSumLogGainQ7, s.libSumLogGainQ7))
		}
		if s.goLastGainIdx != s.libLastGainIdx {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("lastGainIdx: go=%d lib=%d", s.goLastGainIdx, s.libLastGainIdx))
		}
		if s.goSignalType != s.libSignalType {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("signalType: go=%d lib=%d", s.goSignalType, s.libSignalType))
		}
		if s.goPrevLag != s.libPrevLag {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("prevLag: go=%d lib=%d", s.goPrevLag, s.libPrevLag))
		}
		if s.goNSQRandSeed != s.libNSQRandSeed {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("nsqRandSeed: go=%d lib=%d", s.goNSQRandSeed, s.libNSQRandSeed))
		}
		if s.goNSQPrevGainQ16 != s.libNSQPrevGainQ16 {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("nsqPrevGainQ16: go=%d lib=%d", s.goNSQPrevGainQ16, s.libNSQPrevGainQ16))
		}
		if s.goSpeechQ8 != s.libSpeechQ8 {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("speechQ8: go=%d lib=%d", s.goSpeechQ8, s.libSpeechQ8))
		}
		if s.goInputTiltQ15 != s.libInputTiltQ15 {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("inputTiltQ15: go=%d lib=%d", s.goInputTiltQ15, s.libInputTiltQ15))
		}
		if s.goNSQXQHash != s.libNSQXQHash {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("nsqXQHash: go=%016x lib=%016x", s.goNSQXQHash, s.libNSQXQHash))
		}
		if s.goPitchBufHash != s.libPitchBufHash {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("pitchBufHash: go=%016x lib=%016x", s.goPitchBufHash, s.libPitchBufHash))
		}
		if s.goPitchWinHash != s.libPitchWinHash {
			diverged = true
			diffs = append(diffs, fmt.Sprintf("pitchWinHash: go=%016x lib=%016x", s.goPitchWinHash, s.libPitchWinHash))
		}

		if diverged && firstDivergence < 0 {
			firstDivergence = i
		}

		status := "MATCH"
		if !packetMatch {
			status = fmt.Sprintf("DIFFERS (go=%d lib=%d bytes)", goLen, libLen)
		}

		// Log all frames, but with extra detail around divergence
		if !packetMatch || diverged || (firstDivergence >= 0 && i <= firstDivergence+5) || i < 3 {
			t.Logf("Frame %2d: packet %s", i, status)
			if diverged {
				for _, d := range diffs {
					t.Logf("  STATE DIFF: %s", d)
				}
			}
			if !packetMatch && !diverged {
				t.Logf("  (packets differ but all tracked state matches)")
			}
		}
	}

	if firstDivergence >= 0 {
		t.Logf("\nFirst STATE divergence at frame %d", firstDivergence)
	} else {
		t.Logf("\nAll tracked state matches across all %d frames", numFrames)
	}

	// --- Summary: packet match stats ---
	matchCount := 0
	for i := 0; i < numFrames; i++ {
		if len(goPackets[i]) == len(libPackets[i]) {
			match := true
			for j := 0; j < len(goPackets[i]); j++ {
				if goPackets[i][j] != libPackets[i][j] {
					match = false
					break
				}
			}
			if match {
				matchCount++
			}
		}
	}
	t.Logf("Packet match: %d/%d frames identical", matchCount, numFrames)
}

// TestSILKEncoderQualityCompare encodes with both gopus and libopus SILK encoders,
// decodes both with gopus decoder, and compares quality frame by frame.
// This identifies whether the quality gap is in the encoder or the measurement.
func TestSILKEncoderQualityCompare(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960 // 20ms at 48kHz
		bitrate    = 32000
		numFrames  = 50
	)

	totalSamples := numFrames * frameSize * channels
	original := generateMultiFreqSignal(totalSamples)

	// --- gopus encoder ---
	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	goPackets := make([][]byte, numFrames)
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		pcm64 := make([]float64, frameSize)
		for j := 0; j < frameSize; j++ {
			pcm64[j] = float64(original[start+j])
		}
		packet, err := goEnc.Encode(pcm64, frameSize)
		if err != nil {
			t.Fatalf("gopus encode frame %d: %v", i, err)
		}
		cp := make([]byte, len(packet))
		copy(cp, packet)
		goPackets[i] = cp
	}

	// --- libopus encoder ---
	libEnc, err := NewLibopusEncoder(sampleRate, channels, OpusApplicationRestrictedSilk)
	if err != nil || libEnc == nil {
		t.Fatal("Could not create libopus encoder")
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(OpusBandwidthWideband)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	libPackets := make([][]byte, numFrames)
	for i := 0; i < numFrames; i++ {
		start := i * frameSize
		pcm := original[start : start+frameSize]
		pkt, n := libEnc.EncodeFloat(pcm, frameSize)
		if n <= 0 {
			t.Fatalf("libopus encode frame %d: returned %d", i, n)
		}
		libPackets[i] = pkt
	}

	// --- Log packet size comparison ---
	goTotalBytes := 0
	libTotalBytes := 0
	for i := 0; i < numFrames; i++ {
		goTotalBytes += len(goPackets[i])
		libTotalBytes += len(libPackets[i])
	}
	t.Logf("Packet sizes: gopus avg=%.1f bytes, libopus avg=%.1f bytes",
		float64(goTotalBytes)/float64(numFrames), float64(libTotalBytes)/float64(numFrames))

	// --- Decode both with gopus decoder ---
	goDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	libDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	goDecoded := make([]float32, 0, totalSamples)
	libDecoded := make([]float32, 0, totalSamples)
	pcmBuf := make([]float32, frameSize*channels)

	for i := 0; i < numFrames; i++ {
		// Decode gopus packet
		n, err := goDec.Decode(goPackets[i], pcmBuf)
		if err != nil {
			t.Fatalf("Decode gopus packet %d: %v", i, err)
		}
		goDecoded = append(goDecoded, pcmBuf[:n*channels]...)

		// Decode libopus packet
		n, err = libDec.Decode(libPackets[i], pcmBuf)
		if err != nil {
			t.Fatalf("Decode libopus packet %d: %v", i, err)
		}
		libDecoded = append(libDecoded, pcmBuf[:n*channels]...)
	}

	t.Logf("Decoded: gopus=%d samples, libopus=%d samples", len(goDecoded), len(libDecoded))

	// --- Compute overall quality for both ---
	compareLen := len(original)
	if len(goDecoded) < compareLen {
		compareLen = len(goDecoded)
	}
	if len(libDecoded) < compareLen {
		compareLen = len(libDecoded)
	}

	goQ, goDelay := computeQualityFloat32WithDelay(goDecoded[:compareLen], original[:compareLen], 960)
	libQ, libDelay := computeQualityFloat32WithDelay(libDecoded[:compareLen], original[:compareLen], 960)

	goSNR := (goQ/100.0*48.0 + 48.0)
	libSNR := (libQ/100.0*48.0 + 48.0)

	t.Logf("Overall quality:")
	t.Logf("  gopus  encoded -> gopus decoded: Q=%.2f, SNR=%.2f dB, delay=%d", goQ, goSNR, goDelay)
	t.Logf("  libopus encoded -> gopus decoded: Q=%.2f, SNR=%.2f dB, delay=%d", libQ, libSNR, libDelay)
	t.Logf("  Quality gap: %.2f dB SNR", libSNR-goSNR)

	// --- Per-frame comparison (summary only for differing frames) ---
	t.Logf("\nPer-frame packet comparison (differing frames only):")
	for i := 0; i < numFrames; i++ {
		fullMatch := len(goPackets[i]) == len(libPackets[i])
		if fullMatch {
			for j := 0; j < len(goPackets[i]); j++ {
				if goPackets[i][j] != libPackets[i][j] {
					fullMatch = false
					break
				}
			}
		}
		if !fullMatch {
			firstDiffByte := -1
			minLen := len(goPackets[i])
			if len(libPackets[i]) < minLen {
				minLen = len(libPackets[i])
			}
			for j := 0; j < minLen; j++ {
				if goPackets[i][j] != libPackets[i][j] {
					firstDiffByte = j
					break
				}
			}
			t.Logf("  Frame %2d: gopus=%3d bytes, libopus=%3d bytes, first diff at byte %d",
				i, len(goPackets[i]), len(libPackets[i]), firstDiffByte)
		}
	}

	// --- Per-frame decoded SNR ---
	t.Logf("\nPer-frame decoded audio comparison:")
	for i := 0; i < numFrames; i++ {
		origStart := i * frameSize
		origEnd := origStart + frameSize
		if origEnd > len(original) {
			break
		}

		goStart := goDelay + i*frameSize
		goEnd := goStart + frameSize
		libStart := libDelay + i*frameSize
		libEnd := libStart + frameSize

		if goEnd > len(goDecoded) || libEnd > len(libDecoded) || goStart < 0 || libStart < 0 {
			continue
		}

		origFrame := original[origStart:origEnd]
		goFrame := goDecoded[goStart:goEnd]
		libFrame := libDecoded[libStart:libEnd]

		goFrameSNR := frameSNR(goFrame, origFrame)
		libFrameSNR := frameSNR(libFrame, origFrame)
		goLibSNR := frameSNR(goFrame, libFrame) // compare both decoded against each other

		t.Logf("  Frame %2d: go-SNR=%6.1f dB, lib-SNR=%6.1f dB, go-vs-lib=%6.1f dB",
			i, goFrameSNR, libFrameSNR, goLibSNR)
	}
}

func computeQualityFloat32WithDelay(decoded, reference []float32, maxDelay int) (q float64, delay int) {
	if len(decoded) == 0 || len(reference) == 0 {
		return math.Inf(-1), 0
	}

	bestQ := math.Inf(-1)
	bestDelay := 0

	for d := 0; d <= maxDelay; d++ {
		var signalPower, noisePower float64
		count := 0
		margin := 120
		for i := margin; i < len(reference)-margin; i++ {
			decIdx := i + d
			if decIdx >= margin && decIdx < len(decoded)-margin {
				ref := float64(reference[i])
				dec := float64(decoded[decIdx])
				signalPower += ref * ref
				noise := dec - ref
				noisePower += noise * noise
				count++
			}
		}
		if count > 0 && signalPower > 0 && noisePower > 0 {
			snr := 10.0 * math.Log10(signalPower/noisePower)
			candidateQ := (snr - 48.0) * (100.0 / 48.0)
			if candidateQ > bestQ {
				bestQ = candidateQ
				bestDelay = d
			}
		}
	}

	return bestQ, bestDelay
}

func frameSNR(decoded, reference []float32) float64 {
	if len(decoded) == 0 || len(reference) == 0 {
		return math.Inf(-1)
	}
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}
	var sigPow, noisePow float64
	for i := 0; i < n; i++ {
		ref := float64(reference[i])
		dec := float64(decoded[i])
		sigPow += ref * ref
		noise := dec - ref
		noisePow += noise * noise
	}
	if sigPow == 0 || noisePow == 0 {
		if noisePow == 0 {
			return 999.0
		}
		return math.Inf(-1)
	}
	return 10.0 * math.Log10(sigPow/noisePow)
}
