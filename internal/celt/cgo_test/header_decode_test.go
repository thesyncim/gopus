// Package cgo decodes headers from both encoders.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestCompareHeaders decodes and compares headers from both encoders.
func TestCompareHeaders(t *testing.T) {
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

	t.Log("=== Header Comparison ===")
	t.Logf("gopus packet: %d bytes", len(goPacket))
	t.Logf("libopus payload: %d bytes", len(libPayload))

	// Decode libopus header
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	silenceLib := rdLib.DecodeBit(15)
	postfilterLib := rdLib.DecodeBit(1)
	transientLib := rdLib.DecodeBit(3)
	intraLib := rdLib.DecodeBit(3)

	t.Logf("libopus header: silence=%d postfilter=%d transient=%d intra=%d",
		silenceLib, postfilterLib, transientLib, intraLib)
	t.Logf("libopus tell after header: %d bits", rdLib.Tell())

	// Decode gopus header
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

	silenceGo := rdGo.DecodeBit(15)
	postfilterGo := rdGo.DecodeBit(1)
	transientGo := rdGo.DecodeBit(3)
	intraGo := rdGo.DecodeBit(3)

	t.Logf("gopus header:   silence=%d postfilter=%d transient=%d intra=%d",
		silenceGo, postfilterGo, transientGo, intraGo)
	t.Logf("gopus tell after header: %d bits", rdGo.Tell())

	// Compare
	if silenceLib != silenceGo {
		t.Logf("MISMATCH: silence (lib=%d, go=%d)", silenceLib, silenceGo)
	}
	if postfilterLib != postfilterGo {
		t.Logf("MISMATCH: postfilter (lib=%d, go=%d)", postfilterLib, postfilterGo)
	}
	if transientLib != transientGo {
		t.Logf("MISMATCH: transient (lib=%d, go=%d)", transientLib, transientGo)
	}
	if intraLib != intraGo {
		t.Logf("MISMATCH: intra (lib=%d, go=%d)", intraLib, intraGo)
	}

	// Show first bytes
	t.Log("")
	t.Log("=== First Bytes ===")
	t.Log("Byte | gopus | libopus | Match")
	for i := 0; i < 5 && i < len(goPacket) && i < len(libPayload); i++ {
		match := "OK"
		if goPacket[i] != libPayload[i] {
			match = "DIFF"
		}
		t.Logf("  %2d | 0x%02x  |  0x%02x   | %s", i, goPacket[i], libPayload[i], match)
	}
}
