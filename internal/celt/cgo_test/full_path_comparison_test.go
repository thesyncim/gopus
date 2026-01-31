// Package cgo compares the full encoder path with isolated components.
// Agent 23: Identify exactly where full encoder diverges from isolated test.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestFullPathVsIsolatedComponents compares full encoder with isolated component test
func TestFullPathVsIsolatedComponents(t *testing.T) {
	t.Log("=== Agent 23: Full Path vs Isolated Components ===")
	t.Log("")

	frameSize := 960
	channels := 1
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm := make([]float64, frameSize*channels)
	pcm32 := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		sample := 0.5 * math.Sin(2.0*math.Pi*440*float64(i)/48000.0)
		pcm[i] = sample
		pcm32[i] = float32(sample)
	}

	// === Method 1: Full encoder via gopus.NewEncoder ===
	t.Log("=== Method 1: Full encoder via gopus.NewEncoder ===")
	gopusEnc, err := gopus.NewEncoder(48000, channels, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("Failed to create gopus encoder: %v", err)
	}
	_ = gopusEnc.SetBitrate(bitrate)
	gopusEnc.SetFrameSize(frameSize)

	gopusPacket := make([]byte, 4000)
	gopusLen, err := gopusEnc.Encode(pcm32, gopusPacket)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	t.Logf("Full encoder TOC: 0x%02X", gopusPacket[0])
	t.Logf("Full encoder payload (bytes 1-10): %02x", gopusPacket[1:minFP(11, gopusLen)])
	t.Log("")

	// === Method 2: Direct CELT encoder via celt.EncodeFrame ===
	t.Log("=== Method 2: Direct CELT encoder via celt.EncodeFrame ===")
	celtEnc := celt.NewEncoder(channels)
	celtEnc.Reset()
	celtEnc.SetBitrate(bitrate)

	celtPayload, err := celtEnc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("CELT encode failed: %v", err)
	}

	t.Logf("CELT encoder output (bytes 0-10): %02x", celtPayload[:minFP(10, len(celtPayload))])
	t.Log("")

	// === Method 3: Manual step-by-step encoding ===
	t.Log("=== Method 3: Manual step-by-step encoding ===")
	enc := celt.NewEncoder(channels)
	enc.Reset()
	enc.SetBitrate(bitrate)

	// Apply pre-emphasis
	preemph := enc.ApplyPreemphasisWithScaling(pcm)

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Run transient detection
	overlap := celt.Overlap
	transientInput := make([]float64, overlap+frameSize)
	copy(transientInput[overlap:], preemph)
	result := enc.TransientAnalysis(transientInput, frameSize+overlap, false)

	// Force transient for frame 0
	transient := result.IsTransient
	if enc.FrameCount() == 0 && lm > 0 {
		transient = true
	}

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	t.Logf("transient=%v, shortBlocks=%d, lm=%d", transient, shortBlocks, lm)

	// MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)

	// Band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Set up range encoder
	targetBits := bitrate * frameSize / 48000
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Encode header flags
	re.EncodeBit(0, 15) // silence=0
	re.EncodeBit(0, 1)  // postfilter=0
	transientBit := 0
	if transient {
		transientBit = 1
	}
	re.EncodeBit(transientBit, 3) // transient
	re.EncodeBit(0, 3)            // intra=0

	t.Logf("After header: tell=%d bits", re.Tell())

	// Encode coarse energy using encoder
	enc.SetRangeEncoder(re)
	enc.SetFrameBitsForTest(targetBits)

	intra := false
	quantizedEnergies := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	t.Logf("After coarse energy: tell=%d bits", re.Tell())
	_ = quantizedEnergies

	// Get the bytes so far
	manualBytes := re.Done()
	t.Logf("Manual encoding output (bytes 0-10): %02x", manualBytes[:minFP(10, len(manualBytes))])
	t.Log("")

	// === Compare all three outputs ===
	t.Log("=== Comparison ===")
	t.Log("")

	// Compare full encoder payload (skip TOC) with CELT encoder
	fullPayload := gopusPacket[1:gopusLen]
	t.Logf("Full encoder payload: %02x", fullPayload[:minFP(10, len(fullPayload))])
	t.Logf("CELT encoder output:  %02x", celtPayload[:minFP(10, len(celtPayload))])
	t.Logf("Manual encoding:      %02x", manualBytes[:minFP(10, len(manualBytes))])
	t.Log("")

	// Check if CELT encoder matches full encoder payload
	celtMatch := true
	for i := 0; i < minFP(len(fullPayload), len(celtPayload)); i++ {
		if fullPayload[i] != celtPayload[i] {
			celtMatch = false
			t.Logf("CELT vs Full: First diff at byte %d (full=0x%02X, celt=0x%02X)",
				i, fullPayload[i], celtPayload[i])
			break
		}
	}
	if celtMatch {
		t.Log("CELT encoder matches full encoder payload")
	}

	// Check if manual matches CELT
	manualMatch := true
	for i := 0; i < minFP(len(manualBytes), len(celtPayload)); i++ {
		if manualBytes[i] != celtPayload[i] {
			manualMatch = false
			t.Logf("Manual vs CELT: First diff at byte %d (celt=0x%02X, manual=0x%02X)",
				i, celtPayload[i], manualBytes[i])
			break
		}
	}
	if manualMatch {
		t.Log("Manual encoding matches CELT encoder")
	}

	// Get libopus output for comparison
	t.Log("")
	t.Log("=== libopus comparison ===")
	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)
	libEnc.SetSignal(OpusSignalMusic)

	libBytes, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen < 0 {
		t.Fatalf("libopus encode failed: %d", libLen)
	}

	libPayload := libBytes[1:]
	t.Logf("libopus payload:      %02x", libPayload[:minFP(10, len(libPayload))])
	t.Log("")

	// Find first difference between gopus full encoder and libopus
	firstDiff := -1
	for i := 0; i < minFP(len(fullPayload), len(libPayload)); i++ {
		if fullPayload[i] != libPayload[i] {
			firstDiff = i
			break
		}
	}
	if firstDiff >= 0 {
		t.Logf("Full encoder vs libopus: First diff at byte %d (gopus=0x%02X, libopus=0x%02X)",
			firstDiff, fullPayload[firstDiff], libPayload[firstDiff])
	} else {
		t.Log("Full encoder matches libopus!")
	}
}

func minFP(a, b int) int {
	if a < b {
		return a
	}
	return b
}
