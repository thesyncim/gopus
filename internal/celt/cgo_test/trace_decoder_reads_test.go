//go:build trace
// +build trace

// Package cgo traces how many bytes the decoder reads.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceDecoderReads traces how many bytes the decoder reads.
func TestTraceDecoderReads(t *testing.T) {
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

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Log("=== Bytes comparison ===")
	for i := 0; i <= 12; i++ {
		if i < len(libPayload) && i < len(goPacket) {
			marker := ""
			if libPayload[i] != goPacket[i] {
				marker = " <-- DIFFERS"
			}
			t.Logf("Byte %2d: lib=%02X go=%02X%s", i, libPayload[i], goPacket[i], marker)
		}
	}

	t.Log("")
	t.Log("=== Decoding with byte offset tracking ===")

	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

	logState := func(label string) {
		t.Logf("%s: lib(tell=%d bytesUsed=%d rng=%08X val=%08X) go(tell=%d bytesUsed=%d rng=%08X val=%08X)",
			label,
			rdLib.Tell(), rdLib.BytesUsed(), rdLib.Range(), rdLib.Val(),
			rdGo.Tell(), rdGo.BytesUsed(), rdGo.Range(), rdGo.Val())
	}

	logState("Init")

	rdLib.DecodeBit(15)
	rdGo.DecodeBit(15)
	logState("After silence")

	rdLib.DecodeBit(1)
	rdGo.DecodeBit(1)
	logState("After postfilter")

	rdLib.DecodeBit(3)
	rdGo.DecodeBit(3)
	logState("After transient")

	rdLib.DecodeBit(3)
	rdGo.DecodeBit(3)
	logState("After intra")

	// Coarse energy
	decLib := celt.NewDecoder(1)
	decGo := celt.NewDecoder(1)
	decLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, false, lm)
	decGo.DecodeCoarseEnergyWithDecoder(rdGo, nbBands, false, lm)
	logState("After coarse")

	// Key question: at tell=50, how many bytes have been read?
	// If offs > 8, then byte 8 has been read, and that's where the difference is

	t.Log("")
	t.Logf("At tell=50, lib bytesUsed=%d", rdLib.BytesUsed())
	t.Logf("At tell=50, go  bytesUsed=%d", rdGo.BytesUsed())

	t.Log("")
	t.Log("If bytesUsed > 8, then byte 8 (the first differing byte) has been read.")
	t.Log("This would explain why val differs even at tell=50.")
}
