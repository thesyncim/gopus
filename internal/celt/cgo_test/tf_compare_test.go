// Package cgo compares TF decisions between gopus and libopus packets.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestTFCompareLibopus(t *testing.T) {
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

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

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

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)

	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Decode up to TF stage for both packets
	libTF, libTransient := decodeTFFromPacket(libPacket[1:], nbBands, lm) // skip TOC
	goTF, goTransient := decodeTFFromPacket(goPacket, nbBands, lm)

	if libTransient != goTransient {
		t.Fatalf("transient mismatch: libopus=%v gopus=%v", libTransient, goTransient)
	}

	// Compare TF decisions
	mismatch := 0
	for i := 0; i < nbBands; i++ {
		if libTF[i] != goTF[i] {
			mismatch++
			if mismatch <= 5 {
				t.Logf("TF mismatch band %d: libopus=%d gopus=%d", i, libTF[i], goTF[i])
			}
		}
	}
	if mismatch == 0 {
		t.Log("TF decisions match for all bands")
	} else {
		t.Fatalf("TF mismatches: %d/%d bands", mismatch, nbBands)
	}
}

func decodeTFFromPacket(payload []byte, nbBands, lm int) ([]int, bool) {
	rd := &rangecoding.Decoder{}
	rd.Init(payload)

	_ = rd.DecodeBit(15) // silence
	_ = rd.DecodeBit(1)  // postfilter

	transient := false
	if lm > 0 {
		transient = rd.DecodeBit(3) == 1
	}
	intra := rd.DecodeBit(3) == 1

	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)
	_ = dec.DecodeCoarseEnergy(nbBands, intra, lm)

	tfRes := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, transient, tfRes, lm, rd)
	return tfRes, transient
}
