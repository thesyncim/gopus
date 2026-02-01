//go:build trace
// +build trace

// Package cgo traces the final follower values to find the exact divergence.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTraceFollowerFinalValues traces the final follower values used for boost computation.
func TestTraceFollowerFinalValues(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	// Encode with libopus first (to establish what the correct band energies are)
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	_ = libPacket

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goEnc.EncodeFrame(pcm64, frameSize)
	goDynalloc := goEnc.GetLastDynalloc()

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Summarize
	t.Logf("Frame: nbBands=%d, lm=%d", nbBands, lm)
	t.Logf("Gopus offsets[:10]=%v", goDynalloc.Offsets[:10])

	// Expected offsets from libopus (decoded from packet):
	// Band 1: 3 boosts = offsets[1] = 3
	// Band 2: 2 boosts = offsets[2] = 2
	// Band 3: 2 boosts = offsets[3] = 2
	// Band 4: 2 boosts = offsets[4] = 2
	// Band 5: 1 boost = offsets[5] = 1
	// Band 6: 1 boost = offsets[6] = 1
	// Band 7: 0 boosts = offsets[7] = 0

	libExpected := []int{0, 3, 2, 2, 2, 1, 1, 0, 0, 0}

	t.Log("")
	t.Log("Comparison of offsets (count of boosts):")
	for i := 0; i < 10; i++ {
		width := celt.ScaledBandWidth(i, 120<<lm)
		diff := ""
		if goDynalloc.Offsets[i] != libExpected[i] {
			diff = " *** DIFF ***"
		}
		t.Logf("Band %d: width=%d lib=%d go=%d%s", i, width, libExpected[i], goDynalloc.Offsets[i], diff)
	}

	// For the problem band (band 2), trace what follower value would produce each offset
	// boost = int(follower * width / 6) where width=8
	// boost = int(follower * 8 / 6) = int(follower * 1.333)
	// For boost=1: 0.75 <= follower < 1.5
	// For boost=2: 1.5 <= follower < 2.25

	t.Log("")
	t.Log("For band 2 (width=8):")
	t.Log("  offsets[2]=1 requires: 0.75 <= follower < 1.5")
	t.Log("  offsets[2]=2 requires: 1.5 <= follower < 2.25")
	t.Log("  offsets[2]=3 requires: 2.25 <= follower < 3.0")
}
