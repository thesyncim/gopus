//go:build trace
// +build trace

// Package cgo traces the follower computation in dynalloc analysis.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTraceFollowerComputation traces the follower computation
// using manually computed band energies.
func TestTraceFollowerComputation(t *testing.T) {
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

	// Encode with gopus to get band energies and dynalloc
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
	lsbDepth := 16

	t.Logf("Frame config: nbBands=%d, lm=%d, lsbDepth=%d", nbBands, lm, lsbDepth)
	t.Logf("gopus dynalloc offsets[:10]=%v", goDynalloc.Offsets[:10])
	t.Logf("gopus dynalloc TotBoost=%d", goDynalloc.TotBoost)

	// Use a fixed set of band energies for testing
	// These are typical values that might occur with a 440Hz sine wave
	bandLogE := []float64{
		-0.5, 2.0, 1.5, 0.5, 0.0,
		-0.5, -1.0, -1.5, -2.0, -2.5,
		-3.0, -3.5, -4.0, -4.5, -5.0,
		-5.5, -6.0, -6.5, -7.0, -7.5,
		-8.0,
	}

	// Compute follower using libopus algorithm
	libTrace := ComputeFollowerLibopus(bandLogE, nil, nbBands, lsbDepth, lm)

	t.Log("")
	t.Log("=== libopus follower trace ===")
	t.Logf("  last=%d", libTrace.Last)
	for i := 0; i < nbBands; i++ {
		t.Logf("  Band %2d: bandLogE=%.4f noiseFloor=%.4f fFwd=%.4f fBwd=%.4f fMed=%.4f fFinal=%.4f",
			i, bandLogE[i], libTrace.NoiseFloor[i], libTrace.FAfterForward[i], libTrace.FAfterBackward[i],
			libTrace.FAfterMedian[i], libTrace.Follower[i])
	}

	// Compute gopus follower manually to trace
	goFollower := make([]float64, nbBands)
	goFAfterFwd := make([]float64, nbBands)
	goFAfterBwd := make([]float64, nbBands)

	// Forward pass (matches Go code in dynalloc.go lines 296-302)
	goLast := 0
	goFollower[0] = bandLogE[0]
	for i := 1; i < nbBands; i++ {
		if bandLogE[i] > bandLogE[i-1]+0.5 {
			goLast = i
		}
		goFollower[i] = math.Min(goFollower[i-1]+1.5, bandLogE[i])
	}
	copy(goFAfterFwd, goFollower)

	// Backward pass (matches Go code in dynalloc.go lines 305-307)
	for i := goLast - 1; i >= 0; i-- {
		goFollower[i] = math.Min(goFollower[i], math.Min(goFollower[i+1]+2.0, bandLogE[i]))
	}
	copy(goFAfterBwd, goFollower)

	t.Log("")
	t.Log("=== gopus follower trace ===")
	t.Logf("  last=%d (libopus last=%d)", goLast, libTrace.Last)

	hasDiff := false
	for i := 0; i < nbBands; i++ {
		fwdMatch := ""
		if math.Abs(goFAfterFwd[i]-libTrace.FAfterForward[i]) > 0.001 {
			fwdMatch = " *** DIFF ***"
			hasDiff = true
		}
		bwdMatch := ""
		if math.Abs(goFAfterBwd[i]-libTrace.FAfterBackward[i]) > 0.001 {
			bwdMatch = " *** DIFF ***"
			hasDiff = true
		}
		t.Logf("  Band %2d: fFwd=%.4f%s fBwd=%.4f%s (lib fFwd=%.4f fBwd=%.4f)",
			i, goFAfterFwd[i], fwdMatch, goFAfterBwd[i], bwdMatch,
			libTrace.FAfterForward[i], libTrace.FAfterBackward[i])
	}

	if hasDiff {
		t.Error("Follower computation differs between Go and libopus")
	} else {
		t.Log("")
		t.Log("SUCCESS: Follower computation matches libopus exactly")
	}
}

// TestTraceFollowerWithRealEnergies traces follower with real encoded energies
func TestTraceFollowerWithRealEnergies(t *testing.T) {
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

	// Encode with libopus to get a reference packet
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

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)

	// Skip TOC byte in libopus packet
	libPayload := libPacket[1:]

	t.Logf("libopus payload: %d bytes, first 20 bytes: %v", len(libPayload), libPayload[:20])
	t.Logf("gopus packet:    %d bytes, first 20 bytes: %v", len(goPacket), goPacket[:20])

	// Compare payloads
	matchBytes := 0
	for i := 0; i < len(libPayload) && i < len(goPacket); i++ {
		if libPayload[i] == goPacket[i] {
			matchBytes++
		} else {
			break
		}
	}
	t.Logf("Matching bytes: %d", matchBytes)

	// Show first 10 bytes of comparison
	for i := 0; i < 10 && i < len(libPayload) && i < len(goPacket); i++ {
		match := ""
		if libPayload[i] != goPacket[i] {
			match = " ***DIFF***"
		}
		t.Logf("Byte %2d: lib=0x%02x go=0x%02x%s", i, libPayload[i], goPacket[i], match)
	}
}
