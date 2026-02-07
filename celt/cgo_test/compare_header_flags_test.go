//go:build cgo_libopus
// +build cgo_libopus

// Package cgo compares header flags between gopus and libopus outputs.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestCompareHeaderFlags decodes both gopus and libopus packets to compare flags.
func TestCompareHeaderFlags(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
		pcm32[i] = float32(val)
	}

	t.Log("=== Compare Header Flags: gopus vs libopus ===")
	t.Log("")

	// Encode with gopus CELT encoder
	gopusEnc := celt.NewEncoder(1)
	gopusEnc.SetBitrate(bitrate)
	gopusPacket, err := gopusEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
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

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	t.Logf("Gopus packet: %d bytes, first byte: 0x%02X", len(gopusPacket), gopusPacket[0])
	t.Logf("Libopus packet: %d bytes, TOC: 0x%02X, first payload byte: 0x%02X", len(libPacket), libPacket[0], libPacket[1])
	t.Log("")

	// Decode gopus packet flags
	t.Log("=== Gopus Header Flags ===")
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(gopusPacket)

	goSilence := rdGo.DecodeBit(15)
	t.Logf("silence: %d", goSilence)

	goPostfilter := rdGo.DecodeBit(1)
	t.Logf("postfilter: %d", goPostfilter)
	if goPostfilter == 1 {
		octave := int(rdGo.DecodeUniform(6))
		_ = rdGo.DecodeRawBits(uint(4 + octave))
		_ = rdGo.DecodeRawBits(3)
		_ = rdGo.DecodeICDF([]uint8{2, 1, 0}, 2)
	}

	goTransient := rdGo.DecodeBit(3)
	t.Logf("transient: %d", goTransient)

	goIntra := rdGo.DecodeBit(3)
	t.Logf("intra: %d", goIntra)

	t.Log("")

	// Decode libopus packet flags (skip TOC byte)
	t.Log("=== Libopus Header Flags ===")
	libPayload := libPacket[1:]
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	libSilence := rdLib.DecodeBit(15)
	t.Logf("silence: %d", libSilence)

	libPostfilter := rdLib.DecodeBit(1)
	t.Logf("postfilter: %d", libPostfilter)
	if libPostfilter == 1 {
		octave := int(rdLib.DecodeUniform(6))
		_ = rdLib.DecodeRawBits(uint(4 + octave))
		_ = rdLib.DecodeRawBits(3)
		_ = rdLib.DecodeICDF([]uint8{2, 1, 0}, 2)
	}

	libTransient := rdLib.DecodeBit(3)
	t.Logf("transient: %d", libTransient)

	libIntra := rdLib.DecodeBit(3)
	t.Logf("intra: %d", libIntra)

	t.Log("")

	// Compare
	t.Log("=== Comparison ===")
	allMatch := true
	if goSilence != libSilence {
		t.Logf("MISMATCH: silence: gopus=%d, libopus=%d", goSilence, libSilence)
		allMatch = false
	}
	if goPostfilter != libPostfilter {
		t.Logf("MISMATCH: postfilter: gopus=%d, libopus=%d", goPostfilter, libPostfilter)
		allMatch = false
	}
	if goTransient != libTransient {
		t.Logf("MISMATCH: transient: gopus=%d, libopus=%d", goTransient, libTransient)
		allMatch = false
	}
	if goIntra != libIntra {
		t.Logf("MISMATCH: intra: gopus=%d, libopus=%d", goIntra, libIntra)
		allMatch = false
	}

	if allMatch {
		t.Log("All header flags MATCH!")
	} else {
		t.Log("Header flags DIFFER!")
	}

	// Decode a few coarse energy values
	t.Log("")
	t.Log("=== First Few Coarse Energy (Laplace) Values ===")

	// Get probability model for Laplace decoding
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	probModel := celt.GetEProbModel()

	// Create decoders for Laplace values
	gopusDec := celt.NewDecoder(1)
	gopusDec.SetRangeDecoder(rdGo)

	libDec := celt.NewDecoder(1)
	libDec.SetRangeDecoder(rdLib)

	t.Logf("Comparing first 5 qi values (nbBands=%d, lm=%d):", nbBands, lm)
	for band := 0; band < 5 && band < nbBands; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}

		// Determine which probability to use based on each encoder's intra flag
		goProb := probModel[lm][0]
		if goIntra == 1 {
			goProb = probModel[lm][1]
		}
		goFs := int(goProb[pi]) << 7
		goDecay := int(goProb[pi+1]) << 6

		libProb := probModel[lm][0]
		if libIntra == 1 {
			libProb = probModel[lm][1]
		}
		libFs := int(libProb[pi]) << 7
		libDecay := int(libProb[pi+1]) << 6

		goQi := gopusDec.DecodeLaplaceTest(goFs, goDecay)
		libQi := libDec.DecodeLaplaceTest(libFs, libDecay)

		match := ""
		if goQi != libQi {
			match = " <-- DIFFER"
		}
		t.Logf("  Band %d: gopus qi=%d, libopus qi=%d%s", band, goQi, libQi, match)
	}
}
