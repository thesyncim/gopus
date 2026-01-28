package celt

import (
	"math"
	"testing"
)

func TestVBRDebug(t *testing.T) {
	// Create a 440Hz sine wave at 0.5 amplitude
	frameSize := 960
	channels := 1
	bitrate := 64000

	pcm := make([]float64, frameSize*channels)
	sampleRate := 48000.0
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440.0*float64(i)/sampleRate)
	}

	// Create encoder
	enc := NewEncoder(channels)
	enc.SetBitrate(bitrate)
	enc.SetComplexity(10)

	t.Log("=== CELT VBR Debug Test ===")
	t.Logf("Bitrate: %d, FrameSize: %d, Channels: %d", bitrate, frameSize, channels)

	// Manual VBR calculation trace
	baseBits := bitrate * frameSize / 48000
	t.Logf("baseBits = %d * %d / 48000 = %d bits = %d bytes", bitrate, frameSize, baseBits, baseBits/8)

	vbrRateQ3 := baseBits << bitRes
	t.Logf("vbrRateQ3 = %d << %d = %d", baseBits, bitRes, vbrRateQ3)

	overheadQ3 := (40*channels + 20) << bitRes
	t.Logf("overheadQ3 = (40*%d + 20) << %d = %d", channels, bitRes, overheadQ3)

	baseTargetQ3 := vbrRateQ3 - overheadQ3
	t.Logf("baseTargetQ3 = %d - %d = %d", vbrRateQ3, overheadQ3, baseTargetQ3)

	mode := GetModeConfig(frameSize)
	lm := mode.LM
	t.Logf("LM = %d", lm)

	calibration := 19 << lm
	t.Logf("calibration = 19 << %d = %d", lm, calibration)

	// First, test computeTargetBits directly
	targetBits := enc.computeTargetBits(frameSize)
	t.Logf("computeTargetBits returned: %d bits = %d bytes", targetBits, targetBits/8)

	// Expected without VBR boost:
	// targetQ3 = baseTargetQ3 - calibration = 9760 - 152 = 9608 Q3
	// targetBits = (9608 + 4) >> 3 = 1201 bits
	expectedNoBoost := (baseTargetQ3 - calibration + (1 << (bitRes - 1))) >> bitRes
	t.Logf("Expected (no boost): %d bits = %d bytes", expectedNoBoost, expectedNoBoost/8)

	// Encode
	data, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Error: %v", err)
	}

	t.Logf("Encoded bytes: %d", len(data))
	t.Logf("Final range: 0x%08X", enc.FinalRange())

	// Expected: ~261 bytes for VBR with tonal signal
	// Minimum: 160 bytes (64kbps * 20ms / 8)
	if len(data) < 160 {
		t.Logf("WARNING: Output smaller than expected CBR size!")
	}
}
