//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus"
)

func TestCompareEncoders(t *testing.T) {
	frameSize := 960

	// Generate sine wave
	pcm := make([]float64, frameSize)
	for i := range pcm {
		ti := float64(i) / 48000.0
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== Comparing Low-level vs High-level Encoder ===")

	// Low-level CELT encoder
	celtEnc := celt.NewEncoder(1)
	celtEnc.SetBitrate(64000)
	celtPacket, err := celtEnc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("celt encode failed: %v", err)
	}

	t.Logf("CELT packet: %d bytes", len(celtPacket))
	celtLen := len(celtPacket)
	if celtLen > 20 {
		celtLen = 20
	}
	t.Logf("CELT first 20 bytes: %02X", celtPacket[:celtLen])

	// High-level encoder
	highEnc := encoder.NewEncoder(48000, 1)
	highEnc.SetMode(encoder.ModeCELT)
	highEnc.SetBandwidth(gopus.BandwidthFullband)
	highEnc.SetBitrate(64000)
	highPacket, err := highEnc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("high-level encode failed: %v", err)
	}

	t.Logf("High-level packet: %d bytes", len(highPacket))
	highLen := len(highPacket)
	if highLen > 20 {
		highLen = 20
	}
	t.Logf("High-level first 20 bytes: %02X", highPacket[:highLen])

	// Compare packets
	// Note: high-level includes TOC, celt doesn't
	celtPayload := celtPacket
	highPayload := highPacket
	if len(highPacket) > 0 && highPacket[0] == 0xF8 {
		highPayload = highPacket[1:] // Skip TOC
	}

	t.Log("\n=== Payload Comparison ===")
	t.Log("CELT encoder does NOT include TOC")
	t.Log("High-level encoder INCLUDES TOC")
	t.Log("")
	maxCmp := 10
	if len(celtPayload) < maxCmp {
		maxCmp = len(celtPayload)
	}
	if len(highPayload) < maxCmp {
		maxCmp = len(highPayload)
	}

	t.Log("Byte  CELT      High-level  Match?")
	allMatch := true
	for i := 0; i < maxCmp; i++ {
		match := ""
		if celtPayload[i] == highPayload[i] {
			match = " YES"
		} else {
			allMatch = false
		}
		t.Logf("  %2d:  0x%02X      0x%02X%s", i, celtPayload[i], highPayload[i], match)
	}

	if allMatch {
		t.Log("\nPayloads MATCH - same encoder core")
	} else {
		t.Log("\nPayloads DIFFER - different encoder paths")
	}

	// Decode both and compare
	t.Log("\n=== Decoded Output Comparison ===")
	libDec1, _ := NewLibopusDecoder(48000, 1)
	defer libDec1.Destroy()
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()

	toc := byte(0xF8)
	celtWithTOC := append([]byte{toc}, celtPacket...)
	celtDecoded, _ := libDec1.DecodeFloat(celtWithTOC, frameSize)
	highDecoded, _ := libDec2.DecodeFloat(highPacket, frameSize)

	t.Log("Samples at [400]:")
	t.Logf("  Original:    %.4f", pcm[400])
	t.Logf("  CELT decoded: %.4f", celtDecoded[400])
	t.Logf("  High decoded: %.4f", highDecoded[400])
}
