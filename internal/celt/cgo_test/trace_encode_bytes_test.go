//go:build trace
// +build trace

// Package cgo traces byte-by-byte encoding differences.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceEncodeBytesStepByStep traces encoding step by step.
func TestTraceEncodeBytesStepByStep(t *testing.T) {
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

	// Encode with libopus
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
	libPayload := libPacket[1:]

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Now trace gopus encoding to understand how bytes are produced
	t.Log("=== Gopus encoding trace ===")

	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	re.Shrink(159)

	logState := func(label string) {
		// Get written bytes
		written := re.RangeBytes()
		t.Logf("%s: tell=%d rng=0x%08X val=0x%08X offs=%d",
			label, re.Tell(), re.Range(), re.Val(), written)
		if written > 0 {
			t.Logf("  Bytes so far: %02x", buf[:written])
		}
	}

	logState("Init")

	// Header encoding (matches gopus)
	re.EncodeBit(0, 15) // silence=0
	logState("After silence")

	re.EncodeBit(0, 1) // postfilter=0
	logState("After postfilter")

	re.EncodeBit(1, 3) // transient=1
	logState("After transient")

	re.EncodeBit(0, 3) // intra=0
	logState("After intra (tell should be 6)")

	// Now we need to encode coarse energy
	// First decode what libopus encoded to get the QI values
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)
	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	rdLib.DecodeBit(3)  // transient
	rdLib.DecodeBit(3)  // intra

	// Get the QI values from libopus packet
	goDecLib := celt.NewDecoder(1)
	_ = goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, false, lm)

	// The QI values were decoded, but we need to re-encode them
	// Let's instead compare the bytes after the header

	t.Log("")
	t.Log("=== Byte comparison after header encoding ===")
	t.Log("At this point, both encoders have encoded: silence=0, postfilter=0, transient=1, intra=0")
	t.Log("Tell=6, so only partial first byte written")

	// Now let's look at what happens during coarse energy encoding
	t.Log("")
	t.Log("=== Coarse energy analysis ===")

	// Decode QI values from libopus
	rdLib2 := &rangecoding.Decoder{}
	rdLib2.Init(libPayload)
	rdLib2.DecodeBit(15) // silence
	rdLib2.DecodeBit(1)  // postfilter
	rdLib2.DecodeBit(3)  // transient
	rdLib2.DecodeBit(3)  // intra

	// Now decode coarse energy with QI extraction
	// For intra mode, the model is different
	prevE := make([]float64, nbBands)
	qiLib := make([]int, nbBands)

	// Decode first band (intra=0, so use inter model)
	// The model parameter depends on previous energy
	budget := 8 * 159 // total bits

	t.Logf("Before coarse: tell=%d", rdLib2.Tell())

	// Skip QI decode - it requires proper Laplace decoder
	_ = qiLib
	_ = budget

	// Let's check the full decoded coarse values
	t.Log("")
	t.Log("=== Full coarse decode comparison ===")
	rdLib3 := &rangecoding.Decoder{}
	rdLib3.Init(libPayload)
	rdLib3.DecodeBit(15)
	rdLib3.DecodeBit(1)
	rdLib3.DecodeBit(3)
	rdLib3.DecodeBit(3)
	goDecLib3 := celt.NewDecoder(1)
	coarseLib := goDecLib3.DecodeCoarseEnergyWithDecoder(rdLib3, nbBands, false, lm)
	t.Logf("Lib tell after coarse: %d", rdLib3.Tell())

	rdGo3 := &rangecoding.Decoder{}
	rdGo3.Init(goPacket)
	rdGo3.DecodeBit(15)
	rdGo3.DecodeBit(1)
	rdGo3.DecodeBit(3)
	rdGo3.DecodeBit(3)
	goDecGo3 := celt.NewDecoder(1)
	coarseGo := goDecGo3.DecodeCoarseEnergyWithDecoder(rdGo3, nbBands, false, lm)
	t.Logf("Go  tell after coarse: %d", rdGo3.Tell())

	// Compare decoder states
	t.Logf("Lib rng after coarse: 0x%08X", rdLib3.Range())
	t.Logf("Go  rng after coarse: 0x%08X", rdGo3.Range())
	t.Logf("Lib val after coarse: 0x%08X", rdLib3.Val())
	t.Logf("Go  val after coarse: 0x%08X", rdGo3.Val())

	for i := 0; i < nbBands && i < 10; i++ {
		marker := ""
		diff := math.Abs(coarseLib[i] - coarseGo[i])
		if diff > 0.001 {
			marker = " <-- DIFF"
		}
		t.Logf("Coarse band %d: lib=%.4f go=%.4f%s", i, coarseLib[i], coarseGo[i], marker)
	}

	_ = prevE
}
