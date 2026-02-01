// Package cgo traces raw TF bits to find encoding differences.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceTFRawBits traces raw TF bits to find where the encoding differs.
func TestTraceTFRawBits(t *testing.T) {
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

	// Decode step by step and track decoder state
	t.Log("=== Step-by-step decoding comparison ===")

	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

	logCompare := func(step string) {
		rngMatch := rdLib.Range() == rdGo.Range()
		valMatch := rdLib.Val() == rdGo.Val()
		tellMatch := rdLib.Tell() == rdGo.Tell()
		marker := ""
		if !rngMatch || !valMatch {
			marker = " <-- DIFFERS"
		}
		t.Logf("%s: tell=%d/%d rng=%08X/%08X val=%08X/%08X%s",
			step, rdLib.Tell(), rdGo.Tell(),
			rdLib.Range(), rdGo.Range(),
			rdLib.Val(), rdGo.Val(), marker)
		_ = tellMatch
	}

	logCompare("Init")

	// Header
	rdLib.DecodeBit(15)
	rdGo.DecodeBit(15)
	logCompare("After silence")

	rdLib.DecodeBit(1)
	rdGo.DecodeBit(1)
	logCompare("After postfilter")

	rdLib.DecodeBit(3)
	rdGo.DecodeBit(3)
	logCompare("After transient")

	rdLib.DecodeBit(3)
	rdGo.DecodeBit(3)
	logCompare("After intra")

	// Coarse energy
	decLib := celt.NewDecoder(1)
	decGo := celt.NewDecoder(1)
	decLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, false, lm)
	decGo.DecodeCoarseEnergyWithDecoder(rdGo, nbBands, false, lm)
	logCompare("After coarse")

	// TF - decode bit by bit
	t.Log("")
	t.Log("=== TF bit-by-bit ===")
	tfBitsLib := make([]int, nbBands)
	tfBitsGo := make([]int, nbBands)

	for i := 0; i < nbBands; i++ {
		tfBitsLib[i] = rdLib.DecodeBit(1)
		tfBitsGo[i] = rdGo.DecodeBit(1)

		if tfBitsLib[i] != tfBitsGo[i] {
			t.Logf("TF bit %d: lib=%d go=%d <-- DIFFERS", i, tfBitsLib[i], tfBitsGo[i])
			t.Logf("  lib state: rng=%08X val=%08X", rdLib.Range(), rdLib.Val())
			t.Logf("  go  state: rng=%08X val=%08X", rdGo.Range(), rdGo.Val())
		}
	}
	logCompare("After TF bits")

	// TF select
	tfSelLib := rdLib.DecodeBit(1)
	tfSelGo := rdGo.DecodeBit(1)
	t.Logf("TF select: lib=%d go=%d", tfSelLib, tfSelGo)
	logCompare("After TF select")

	// Spread
	spreadICDF := []uint8{25, 23, 2, 0}
	spreadLib := rdLib.DecodeICDF(spreadICDF, 5)
	spreadGo := rdGo.DecodeICDF(spreadICDF, 5)
	t.Logf("Spread: lib=%d go=%d", spreadLib, spreadGo)
	logCompare("After spread")

	// Show raw bytes
	t.Log("")
	t.Log("=== Raw bytes ===")
	for i := 0; i <= 12; i++ {
		if i < len(libPayload) && i < len(goPacket) {
			marker := ""
			if libPayload[i] != goPacket[i] {
				marker = " <-- DIFFERS"
			}
			t.Logf("Byte %2d: lib=%02X go=%02X%s", i, libPayload[i], goPacket[i], marker)
		}
	}

	// Now check what libopus debug says vs what we decode
	t.Log("")
	t.Log("=== Analysis ===")
	t.Log("SPREAD_DEBUG shows libopus encodes spread=2")
	t.Logf("But decoded spread from libopus packet is: %d", spreadLib)
	t.Logf("Decoded spread from gopus packet is: %d", spreadGo)

	// This suggests there might be a bug in our decoding, OR
	// libopus is doing something we don't understand
}

// TestCheckSpreadDecoding verifies spread decoding with known values.
func TestCheckSpreadDecoding(t *testing.T) {
	// The spread ICDF is: {25, 23, 2, 0}
	// This represents cumulative probabilities for spread 0,1,2,3
	// ft = 32 (5 bits), so:
	// spread=0: fl=25, fh=32 (prob 7/32)
	// spread=1: fl=23, fh=25 (prob 2/32)
	// spread=2: fl=2, fh=23 (prob 21/32) <-- most likely
	// spread=3: fl=0, fh=2 (prob 2/32)

	spreadICDF := []uint8{25, 23, 2, 0}

	// Create test packets with known spread values
	for testSpread := 0; testSpread <= 3; testSpread++ {
		buf := make([]byte, 256)
		enc := &rangecoding.Encoder{}
		enc.Init(buf)

		// Encode spread value
		enc.EncodeICDF(testSpread, spreadICDF, 5)

		// Finish encoding
		bytes := enc.Done()

		// Decode and verify
		dec := &rangecoding.Decoder{}
		dec.Init(bytes)
		decoded := dec.DecodeICDF(spreadICDF, 5)

		match := ""
		if decoded != testSpread {
			match = " <-- MISMATCH"
		}
		t.Logf("Encoded spread=%d, decoded=%d%s", testSpread, decoded, match)
	}
}
