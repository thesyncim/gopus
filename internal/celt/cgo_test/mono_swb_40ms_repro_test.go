// Package cgo tests reproduction of mono SWB 40ms decoder mismatch.
package cgo

import (
	"math"
	"math/rand"
	"testing"

	gopus "github.com/thesyncim/gopus"
)

// TestMonoSWB40msMismatch reproduces the decoder mismatch found by fuzzing:
// seed=22222, channels=1, frameSize=1920, bitrate=104000, bandwidth=1104 (superwideband)
// Max sample diff: 0.578680 at sample 1817
// Correlation: 0.678976 (threshold: 0.99)
// RMS diff: 0.157299
func TestMonoSWB40msMismatch(t *testing.T) {
	// Exact parameters from the failing fuzz case
	seed := uint64(22222)
	channels := 1
	frameSize := 1920 // 40ms at 48kHz
	bitrate := 104000
	bandwidth := OpusBandwidthSuperwideband // 1104

	t.Logf("Test configuration: seed=%d, ch=%d, fs=%d, br=%d, bw=%d",
		seed, channels, frameSize, bitrate, bandwidth)

	// Generate random audio using seed
	rng := rand.New(rand.NewSource(int64(seed)))
	pcm := generateRandomAudioF32(rng, frameSize*channels)

	// Create libopus encoder
	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	// Configure encoder exactly as in fuzz test
	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(bandwidth)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	// Encode with libopus
	packet, encLen := libEnc.EncodeFloat(pcm, frameSize)
	if encLen <= 0 {
		t.Fatalf("Encoding failed with len=%d", encLen)
	}
	t.Logf("Encoded %d samples to %d bytes", frameSize, encLen)
	t.Logf("Packet TOC: 0x%02x", packet[0])

	// Create libopus decoder (reference)
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	defer libDec.Destroy()

	// Create gopus decoder
	gopusDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	// Decode with libopus (reference)
	libDecoded, libDecLen := libDec.DecodeFloat(packet, frameSize)
	if libDecLen <= 0 {
		t.Fatalf("libopus decode failed: %d", libDecLen)
	}

	// Decode with gopus
	gopusDecoded, err := decodeFloat32(gopusDec, packet)
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}

	expectedSamples := frameSize * channels
	if len(gopusDecoded) != expectedSamples {
		t.Errorf("gopus output length mismatch: got %d, want %d", len(gopusDecoded), expectedSamples)
	}

	// Check for NaN or Inf in gopus output
	for i, s := range gopusDecoded {
		if math.IsNaN(float64(s)) {
			t.Errorf("NaN at sample %d", i)
			return
		}
		if math.IsInf(float64(s), 0) {
			t.Errorf("Inf at sample %d", i)
			return
		}
	}

	// Compare outputs: gopus vs libopus
	result := compareDecoderOutputs(libDecoded[:expectedSamples], gopusDecoded)

	t.Logf("Comparison results:")
	t.Logf("  Max diff: %.6f at sample %d", result.maxDiff, result.maxDiffIdx)
	t.Logf("  RMS diff: %.6f (threshold: %.6f)", result.rmsDiff, RMSDiffThreshold)
	t.Logf("  Correlation: %.6f (threshold: %.6f)", result.correlation, CorrelationThreshold)
	t.Logf("  Mean diff: %.6f", result.meanDiff)

	// Print samples around the max diff
	maxIdx := result.maxDiffIdx
	t.Logf("\nSamples around max diff (sample %d):", maxIdx)
	start := maxIdx - 10
	if start < 0 {
		start = 0
	}
	end := maxIdx + 11
	if end > expectedSamples {
		end = expectedSamples
	}
	for i := start; i < end; i++ {
		diff := libDecoded[i] - gopusDecoded[i]
		marker := ""
		if i == maxIdx {
			marker = " <-- MAX DIFF"
		}
		t.Logf("  [%4d] lib=%.6f gopus=%.6f diff=%.6f%s",
			i, libDecoded[i], gopusDecoded[i], diff, marker)
	}

	// Print statistics for different regions
	t.Logf("\nRegion analysis:")
	regions := []struct {
		name  string
		start int
		end   int
	}{
		{"First 480 (0-479)", 0, 480},
		{"Second 480 (480-959)", 480, 960},
		{"Third 480 (960-1439)", 960, 1440},
		{"Fourth 480 (1440-1919)", 1440, 1920},
	}

	for _, r := range regions {
		regionLib := libDecoded[r.start:r.end]
		regionGo := gopusDecoded[r.start:r.end]
		regionResult := compareDecoderOutputs(regionLib, regionGo)
		t.Logf("  %s: maxDiff=%.6f, corr=%.6f, rmsDiff=%.6f",
			r.name, regionResult.maxDiff, regionResult.correlation, regionResult.rmsDiff)
	}

	// Check thresholds
	if result.maxDiff > MaxSampleDiffThreshold {
		t.Errorf("Max sample diff %.6f exceeds threshold %.6f at sample %d",
			result.maxDiff, MaxSampleDiffThreshold, result.maxDiffIdx)
	}

	if result.correlation < CorrelationThreshold {
		t.Errorf("Correlation %.6f below threshold %.6f",
			result.correlation, CorrelationThreshold)
	}

	if result.rmsDiff > RMSDiffThreshold {
		t.Errorf("RMS diff %.6f exceeds threshold %.6f",
			result.rmsDiff, RMSDiffThreshold)
	}
}

// TestMonoSWB40msPacketDump dumps packet details for investigation
func TestMonoSWB40msPacketDump(t *testing.T) {
	seed := uint64(22222)
	channels := 1
	frameSize := 1920
	bitrate := 104000
	bandwidth := OpusBandwidthSuperwideband

	rng := rand.New(rand.NewSource(int64(seed)))
	pcm := generateRandomAudioF32(rng, frameSize*channels)

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(bandwidth)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	packet, encLen := libEnc.EncodeFloat(pcm, frameSize)
	if encLen <= 0 {
		t.Fatalf("Encoding failed")
	}

	// Parse TOC byte
	toc := packet[0]
	config := (toc >> 3) & 0x1f
	stereoFlag := (toc >> 2) & 0x01
	frameCode := toc & 0x03

	t.Logf("Packet analysis:")
	t.Logf("  Length: %d bytes", encLen)
	t.Logf("  TOC: 0x%02x (config=%d, stereo=%d, frameCode=%d)", toc, config, stereoFlag, frameCode)

	// Determine mode from config
	modeStr := "unknown"
	if config >= 0 && config <= 3 {
		modeStr = "SILK-only NB"
	} else if config >= 4 && config <= 7 {
		modeStr = "SILK-only MB"
	} else if config >= 8 && config <= 11 {
		modeStr = "SILK-only WB"
	} else if config >= 12 && config <= 13 {
		modeStr = "Hybrid SWB"
	} else if config >= 14 && config <= 15 {
		modeStr = "Hybrid FB"
	} else if config >= 16 && config <= 19 {
		modeStr = "CELT-only NB"
	} else if config >= 20 && config <= 23 {
		modeStr = "CELT-only WB"
	} else if config >= 24 && config <= 27 {
		modeStr = "CELT-only SWB"
	} else if config >= 28 && config <= 31 {
		modeStr = "CELT-only FB"
	}
	t.Logf("  Mode: %s", modeStr)

	// Print first 64 bytes hex
	dumpLen := encLen
	if dumpLen > 64 {
		dumpLen = 64
	}
	t.Logf("  First %d bytes:", dumpLen)
	for i := 0; i < dumpLen; i += 16 {
		end := i + 16
		if end > dumpLen {
			end = dumpLen
		}
		hexStr := ""
		for j := i; j < end; j++ {
			hexStr += " " + hexByte(packet[j])
		}
		t.Logf("    %04x:%s", i, hexStr)
	}
}

func hexByte(b byte) string {
	const hex = "0123456789abcdef"
	return string([]byte{hex[b>>4], hex[b&0x0f]})
}

// TestStateComparisonAfterFirstFrame compares decoder state after first frame
func TestStateComparisonAfterFirstFrame(t *testing.T) {
	seed := uint64(22222)
	channels := 1
	bitrate := 104000
	bandwidth := OpusBandwidthSuperwideband

	rng := rand.New(rand.NewSource(int64(seed)))
	pcm := generateRandomAudioF32(rng, 960*channels) // First 960 samples only

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(bandwidth)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	packet, encLen := libEnc.EncodeFloat(pcm, 960)
	if encLen <= 0 {
		t.Fatalf("Encoding failed")
	}
	t.Logf("Packet: %d bytes, TOC=0x%02x", encLen, packet[0])

	// Create decoders
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	defer libDec.Destroy()

	gopusDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	// Decode first frame
	libDecoded, _ := libDec.DecodeFloat(packet, 960)
	gopusDecoded, _ := decodeFloat32(gopusDec, packet)

	// Verify first frame is perfect
	result := compareDecoderOutputs(libDecoded[:960], gopusDecoded)
	t.Logf("First frame: maxDiff=%.6f, corr=%.6f", result.maxDiff, result.correlation)

	// Compare preemph state
	libMem0, _ := libDec.GetPreemphState()
	goState := gopusDec.GetCELTDecoder().PreemphState()
	goMem0 := float32(goState[0])

	t.Logf("\nAfter first frame decode:")
	t.Logf("  libopus preemph_state: %.6f", libMem0)
	t.Logf("  gopus preemph_state:   %.6f", goMem0)
	t.Logf("  Difference: %.6f", math.Abs(float64(goMem0-libMem0)))

	// Get gopus CELT state
	celtDec := gopusDec.GetCELTDecoder()

	// Check overlap buffer (first few samples)
	overlapBuf := celtDec.OverlapBuffer()
	t.Logf("\nOverlap buffer (first 5 samples):")
	for i := 0; i < 5 && i < len(overlapBuf); i++ {
		t.Logf("  overlap[%d] = %.6f", i, overlapBuf[i])
	}

	// Check prevEnergy
	prevEnergy := celtDec.PrevEnergy()
	t.Logf("\nPrevEnergy (first 5 bands):")
	for i := 0; i < 5 && i < len(prevEnergy); i++ {
		t.Logf("  prevEnergy[%d] = %.6f", i, prevEnergy[i])
	}

	// Check RNG state
	t.Logf("\nRNG state: %d", celtDec.RNG())

	// Now verify the issue: decode a SECOND frame
	t.Logf("\n--- Decoding second frame to verify issue persists ---")

	// Generate second frame's audio
	rng2 := rand.New(rand.NewSource(int64(seed)))
	// Skip first 960 samples to match original data generation pattern
	_ = generateRandomAudioF32(rng2, 960*channels)
	pcm2 := generateRandomAudioF32(rng2, 960*channels) // Second 960 samples

	packet2, encLen2 := libEnc.EncodeFloat(pcm2, 960)
	if encLen2 <= 0 {
		t.Fatalf("Encoding second frame failed")
	}

	libDecoded2, _ := libDec.DecodeFloat(packet2, 960)
	gopusDecoded2, _ := decodeFloat32(gopusDec, packet2)

	result2 := compareDecoderOutputs(libDecoded2[:960], gopusDecoded2)
	t.Logf("Second frame: maxDiff=%.6f, corr=%.6f", result2.maxDiff, result2.correlation)

	// Compare preemph state after second frame
	libMem0After, _ := libDec.GetPreemphState()
	goStateAfter := gopusDec.GetCELTDecoder().PreemphState()
	goMem0After := float32(goStateAfter[0])

	t.Logf("\nAfter second frame decode:")
	t.Logf("  libopus preemph_state: %.6f", libMem0After)
	t.Logf("  gopus preemph_state:   %.6f", goMem0After)
	t.Logf("  Difference: %.6f", math.Abs(float64(goMem0After-libMem0After)))
}

// TestMonoSWB40msFrameBoundary examines the frame boundary at sample 960 (where frame 1 ends and frame 2 begins)
func TestMonoSWB40msFrameBoundary(t *testing.T) {
	seed := uint64(22222)
	channels := 1
	frameSize := 1920
	bitrate := 104000
	bandwidth := OpusBandwidthSuperwideband

	rng := rand.New(rand.NewSource(int64(seed)))
	pcm := generateRandomAudioF32(rng, frameSize*channels)

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(bandwidth)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	packet, encLen := libEnc.EncodeFloat(pcm, frameSize)
	if encLen <= 0 {
		t.Fatalf("Encoding failed")
	}

	// Parse packet structure
	t.Logf("Packet length: %d bytes", encLen)
	t.Logf("TOC: 0x%02x", packet[0])

	// For frameCode=1, frames are split equally
	frame1Data := packet[1 : 1+(encLen-1)/2]
	frame2Data := packet[1+(encLen-1)/2 : encLen]
	t.Logf("Frame 1 data: %d bytes", len(frame1Data))
	t.Logf("Frame 2 data: %d bytes", len(frame2Data))

	// Create libopus decoder
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	defer libDec.Destroy()

	// Create gopus decoder
	gopusDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	// Decode full packet with libopus
	libDecoded, libDecLen := libDec.DecodeFloat(packet, frameSize)
	if libDecLen <= 0 {
		t.Fatalf("libopus decode failed")
	}

	// Decode full packet with gopus
	gopusDecoded, err := decodeFloat32(gopusDec, packet)
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}

	// Show samples around frame boundary (sample 960)
	t.Logf("\nSamples around frame boundary (frame1 ends at 959, frame2 starts at 960):")
	for i := 955; i < 965; i++ {
		diff := libDecoded[i] - gopusDecoded[i]
		marker := ""
		if i == 959 {
			marker = " <- end of frame 1"
		} else if i == 960 {
			marker = " <- start of frame 2"
		}
		t.Logf("  [%4d] lib=%.6f gopus=%.6f diff=%.6f%s",
			i, libDecoded[i], gopusDecoded[i], diff, marker)
	}

	// Calculate stats for exactly at and around boundary
	var sumDiffBefore, sumDiffAfter float64
	for i := 0; i < 960; i++ {
		sumDiffBefore += math.Abs(float64(libDecoded[i] - gopusDecoded[i]))
	}
	for i := 960; i < 1920; i++ {
		sumDiffAfter += math.Abs(float64(libDecoded[i] - gopusDecoded[i]))
	}
	t.Logf("\nMean absolute difference:")
	t.Logf("  Frame 1 (samples 0-959): %.6f", sumDiffBefore/960)
	t.Logf("  Frame 2 (samples 960-1919): %.6f", sumDiffAfter/960)
}

// TestSingleFrameSWB tests decoding a single 20ms frame (frameCode=0) to verify CELT works
func TestSingleFrameSWB(t *testing.T) {
	seed := uint64(22222)
	channels := 1
	frameSize := 960 // 20ms single frame
	bitrate := 104000
	bandwidth := OpusBandwidthSuperwideband

	rng := rand.New(rand.NewSource(int64(seed)))
	pcm := generateRandomAudioF32(rng, frameSize*channels)

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(bandwidth)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	packet, encLen := libEnc.EncodeFloat(pcm, frameSize)
	if encLen <= 0 {
		t.Fatalf("Encoding failed")
	}
	t.Logf("Single 20ms packet: %d bytes, TOC=0x%02x", encLen, packet[0])

	// Parse TOC
	toc := packet[0]
	config := (toc >> 3) & 0x1f
	frameCode := toc & 0x03
	t.Logf("Config=%d, frameCode=%d (should be 0 for single frame)", config, frameCode)

	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	defer libDec.Destroy()

	gopusDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	libDecoded, libDecLen := libDec.DecodeFloat(packet, frameSize)
	if libDecLen <= 0 {
		t.Fatalf("libopus decode failed")
	}

	gopusDecoded, err := decodeFloat32(gopusDec, packet)
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}

	result := compareDecoderOutputs(libDecoded[:frameSize], gopusDecoded)
	t.Logf("Single frame comparison:")
	t.Logf("  Max diff: %.6f at sample %d", result.maxDiff, result.maxDiffIdx)
	t.Logf("  Correlation: %.6f", result.correlation)
	t.Logf("  RMS diff: %.6f", result.rmsDiff)

	if result.maxDiff > MaxSampleDiffThreshold {
		t.Errorf("Single frame mismatch! Max diff %.6f > %.6f", result.maxDiff, MaxSampleDiffThreshold)
	}
}

// TestTwoSequential20msFrames tests decoding two separate 20ms packets sequentially
func TestTwoSequential20msFrames(t *testing.T) {
	seed := uint64(22222)
	channels := 1
	frameSize := 960 // 20ms
	bitrate := 104000
	bandwidth := OpusBandwidthSuperwideband

	// Generate audio from ONE rng with 1920 samples (same as the 40ms case)
	// Then split into two 960-sample frames
	rng := rand.New(rand.NewSource(int64(seed)))
	pcmFull := generateRandomAudioF32(rng, 1920*channels)
	pcm1 := pcmFull[:960]
	pcm2 := pcmFull[960:]

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(bandwidth)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	// Encode two separate packets
	packet1, encLen1 := libEnc.EncodeFloat(pcm1, frameSize)
	if encLen1 <= 0 {
		t.Fatalf("Encoding packet 1 failed")
	}
	packet2, encLen2 := libEnc.EncodeFloat(pcm2, frameSize)
	if encLen2 <= 0 {
		t.Fatalf("Encoding packet 2 failed")
	}
	t.Logf("Packet 1: %d bytes, TOC=0x%02x", encLen1, packet1[0])
	t.Logf("Packet 2: %d bytes, TOC=0x%02x", encLen2, packet2[0])

	// Create decoders
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	defer libDec.Destroy()

	gopusDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	// Decode packet 1
	libDecoded1, _ := libDec.DecodeFloat(packet1, frameSize)
	gopusDecoded1, err := decodeFloat32(gopusDec, packet1)
	if err != nil {
		t.Fatalf("gopus decode packet 1 error: %v", err)
	}

	// Decode packet 2
	libDecoded2, _ := libDec.DecodeFloat(packet2, frameSize)
	gopusDecoded2, err := decodeFloat32(gopusDec, packet2)
	if err != nil {
		t.Fatalf("gopus decode packet 2 error: %v", err)
	}

	// Compare packet 1
	result1 := compareDecoderOutputs(libDecoded1[:frameSize], gopusDecoded1)
	t.Logf("Packet 1 comparison:")
	t.Logf("  Max diff: %.6f at sample %d", result1.maxDiff, result1.maxDiffIdx)
	t.Logf("  Correlation: %.6f", result1.correlation)

	// Compare packet 2 (the second frame, uses state from first)
	result2 := compareDecoderOutputs(libDecoded2[:frameSize], gopusDecoded2)
	t.Logf("Packet 2 comparison:")
	t.Logf("  Max diff: %.6f at sample %d", result2.maxDiff, result2.maxDiffIdx)
	t.Logf("  Correlation: %.6f", result2.correlation)

	if result1.maxDiff > MaxSampleDiffThreshold {
		t.Errorf("Packet 1 mismatch! Max diff %.6f > %.6f", result1.maxDiff, MaxSampleDiffThreshold)
	}
	if result2.maxDiff > MaxSampleDiffThreshold {
		t.Errorf("Packet 2 mismatch! Max diff %.6f > %.6f", result2.maxDiff, MaxSampleDiffThreshold)
	}
}

// TestDecodeSamePacketWithFreshDecoders tests if it's encoder state causing the issue
func TestDecodeSamePacketWithFreshDecoders(t *testing.T) {
	seed := uint64(22222)
	channels := 1
	frameSize := 1920
	bitrate := 104000
	bandwidth := OpusBandwidthSuperwideband

	rng := rand.New(rand.NewSource(int64(seed)))
	pcm := generateRandomAudioF32(rng, frameSize*channels)

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(bandwidth)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	packet, encLen := libEnc.EncodeFloat(pcm, frameSize)
	if encLen <= 0 {
		t.Fatalf("Encoding failed")
	}

	// Dump the first few bytes of frame 1 and frame 2
	frame1Start := 1
	frame1End := 1 + (encLen-1)/2
	frame2Start := frame1End
	frame2End := encLen

	t.Logf("Frame 1 first bytes: %02x %02x %02x %02x", packet[frame1Start], packet[frame1Start+1], packet[frame1Start+2], packet[frame1Start+3])
	t.Logf("Frame 2 first bytes: %02x %02x %02x %02x", packet[frame2Start], packet[frame2Start+1], packet[frame2Start+2], packet[frame2Start+3])

	// Decode ONLY frame 1 with fresh decoders (extract just first frame)
	// We can't decode partial multi-frame packets, so let's trace what happens
	// during the multi-frame decode by checking CELT decoder internal state

	// Create fresh decoders for each decode
	for i := 0; i < 2; i++ {
		libDec, err := NewLibopusDecoder(48000, channels)
		if err != nil || libDec == nil {
			t.Fatalf("Failed to create libopus decoder: %v", err)
		}

		gopusDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
		if err != nil {
			t.Fatalf("Failed to create gopus decoder: %v", err)
		}

		libDecoded, _ := libDec.DecodeFloat(packet, frameSize)
		gopusDecoded, _ := decodeFloat32(gopusDec, packet)

		// Get CELT state after decode
		celtDec := gopusDec.GetCELTDecoder()
		t.Logf("Decode %d: CELT preemph_state=[%.6f]",
			i, celtDec.PreemphState()[0])

		result := compareDecoderOutputs(libDecoded[:frameSize], gopusDecoded)
		t.Logf("Decode %d: maxDiff=%.6f, corr=%.6f", i, result.maxDiff, result.correlation)

		libDec.Destroy()
	}

	t.Logf("\nNow test frame 1 vs frame 2 in the same packet separately:")

	// The key insight: in a frameCode=1 packet, both frames share the SAME bitstream
	// but are decoded with SEPARATE range decoder contexts (or should be?)

	// Let's manually check the byte alignment
	_ = frame2End // suppress unused warning
}

// TestDecodeFrame1OnlyAsSingleFrame tests decoding just frame1 data as a single-frame packet
func TestDecodeFrame1OnlyAsSingleFrame(t *testing.T) {
	seed := uint64(22222)
	channels := 1
	bitrate := 104000
	bandwidth := OpusBandwidthSuperwideband

	// Generate 40ms audio (same as failing case)
	rng := rand.New(rand.NewSource(int64(seed)))
	pcm := generateRandomAudioF32(rng, 1920*channels)

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(bandwidth)
	libEnc.SetComplexity(10)
	libEnc.SetVBR(true)

	// Encode ONLY first 960 samples as a single 20ms frame
	packet1, encLen1 := libEnc.EncodeFloat(pcm[:960], 960)
	if encLen1 <= 0 {
		t.Fatalf("Encoding single frame failed")
	}
	t.Logf("Single 20ms packet (first 960 samples): %d bytes, TOC=0x%02x", encLen1, packet1[0])

	// Create decoders
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	defer libDec.Destroy()

	gopusDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	// Decode
	libDecoded1, _ := libDec.DecodeFloat(packet1, 960)
	gopusDecoded1, err := decodeFloat32(gopusDec, packet1)
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}

	result1 := compareDecoderOutputs(libDecoded1[:960], gopusDecoded1)
	t.Logf("Single frame result: maxDiff=%.6f, corr=%.6f", result1.maxDiff, result1.correlation)

	// Now encode the second 960 samples (after encoding first)
	packet2, encLen2 := libEnc.EncodeFloat(pcm[960:1920], 960)
	if encLen2 <= 0 {
		t.Fatalf("Encoding second frame failed")
	}
	t.Logf("Single 20ms packet (second 960 samples): %d bytes, TOC=0x%02x", encLen2, packet2[0])

	// Continue decoding (state should carry forward)
	libDecoded2, _ := libDec.DecodeFloat(packet2, 960)
	gopusDecoded2, err := decodeFloat32(gopusDec, packet2)
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}

	result2 := compareDecoderOutputs(libDecoded2[:960], gopusDecoded2)
	t.Logf("Second frame result: maxDiff=%.6f, corr=%.6f", result2.maxDiff, result2.correlation)

	// Compare with the 40ms case
	t.Logf("\nFor comparison, now encode as 40ms packet:")

	// Re-encode as 40ms (reset encoder first)
	libEnc2, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc2 == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc2.Destroy()

	libEnc2.SetBitrate(bitrate)
	libEnc2.SetBandwidth(bandwidth)
	libEnc2.SetComplexity(10)
	libEnc2.SetVBR(true)

	packet40ms, encLen40ms := libEnc2.EncodeFloat(pcm, 1920)
	if encLen40ms <= 0 {
		t.Fatalf("Encoding 40ms failed")
	}
	t.Logf("40ms packet: %d bytes, TOC=0x%02x", encLen40ms, packet40ms[0])

	// Fresh decoders for 40ms
	libDec40, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec40 == nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	defer libDec40.Destroy()

	gopusDec40, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}

	libDecoded40, _ := libDec40.DecodeFloat(packet40ms, 1920)
	gopusDecoded40, err := decodeFloat32(gopusDec40, packet40ms)
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}

	// Compare first 960 samples
	result40_1 := compareDecoderOutputs(libDecoded40[:960], gopusDecoded40[:960])
	t.Logf("40ms frame1 (0-959): maxDiff=%.6f, corr=%.6f", result40_1.maxDiff, result40_1.correlation)

	// Compare second 960 samples
	result40_2 := compareDecoderOutputs(libDecoded40[960:1920], gopusDecoded40[960:1920])
	t.Logf("40ms frame2 (960-1919): maxDiff=%.6f, corr=%.6f", result40_2.maxDiff, result40_2.correlation)
}
