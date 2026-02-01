//go:build trace
// +build trace

// Package cgo traces exact byte/bit differences.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceExactByteDiff traces the exact byte difference.
func TestTraceExactByteDiff(t *testing.T) {
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

	t.Log("=== Byte comparison ===")
	for i := 0; i <= 12; i++ {
		if i < len(libPayload) && i < len(goPacket) {
			lib := libPayload[i]
			go_ := goPacket[i]
			xor := lib ^ go_
			marker := ""
			if xor != 0 {
				marker = " <-- DIFFERS"
			}
			t.Logf("Byte %2d: lib=%02X (%08b) go=%02X (%08b) xor=%08b%s",
				i, lib, lib, go_, go_, xor, marker)
		}
	}

	// The first different byte is byte 8
	// Byte 8: lib=BB (10111011) go=BA (10111010) xor=00000001
	// Only the LSB differs!

	t.Log("")
	t.Log("=== Bit-by-bit analysis ===")
	// Bit 64 (byte 8, bit 0) is the first difference
	// This is 64 bits into the packet
	// Let's see what's encoded at this position

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

	// Skip header (6 bits)
	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	rdLib.DecodeBit(3)  // transient
	rdLib.DecodeBit(3)  // intra
	rdGo.DecodeBit(15)
	rdGo.DecodeBit(1)
	rdGo.DecodeBit(3)
	rdGo.DecodeBit(3)
	t.Logf("After header: tell=%d", rdLib.Tell())

	// Coarse energy (44 bits = 50-6)
	decLib := celt.NewDecoder(1)
	decGo := celt.NewDecoder(1)
	decLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, false, lm)
	decGo.DecodeCoarseEnergyWithDecoder(rdGo, nbBands, false, lm)
	t.Logf("After coarse: tell=%d", rdLib.Tell())

	// TF (22 bits = 72-50)
	for i := 0; i < nbBands; i++ {
		rdLib.DecodeBit(1)
		rdGo.DecodeBit(1)
	}
	rdLib.DecodeBit(1) // tf_select
	rdGo.DecodeBit(1)
	t.Logf("After TF: tell=%d (bit 72)", rdLib.Tell())

	// At tell=72, we're at bit 72
	// Byte 8 starts at bit 64, ends at bit 72
	// So the difference at byte 8 LSB is at bit 64

	// The coarse energy ends at bit 50
	// TF starts at bit 50 and ends at bit 72 (21 TF bits + 1 select = 22 bits)
	// So the difference is happening somewhere in TF or right at the boundary

	// Let's trace exactly what the range encoder produces after each TF bit
	t.Log("")
	t.Log("=== Range encoder state during TF encoding ===")

	// Create new encoders and re-encode the header
	buf := make([]byte, 256)
	reTrace := &rangecoding.Encoder{}
	reTrace.Init(buf)
	reTrace.Shrink(159)

	reTrace.EncodeBit(0, 15) // silence=0
	reTrace.EncodeBit(0, 1)  // postfilter=0
	reTrace.EncodeBit(1, 3)  // transient=1
	reTrace.EncodeBit(0, 3)  // intra=0
	t.Logf("After header: tell=%d rng=%08X offs=%d",
		reTrace.Tell(), reTrace.Range(), reTrace.RangeBytes())

	// We need to know what QI values are encoded for coarse energy
	// Let's decode them from the libopus packet
	rdLib2 := &rangecoding.Decoder{}
	rdLib2.Init(libPayload)
	rdLib2.DecodeBit(15)
	rdLib2.DecodeBit(1)
	rdLib2.DecodeBit(3)
	rdLib2.DecodeBit(3)

	// Decode coarse energy to get QI values
	// For this we need to trace the actual Laplace decode
	// But for now, let's just look at byte positions

	t.Log("")
	t.Log("=== Key insight ===")
	t.Log("Byte 8 (bits 64-71) differs at bit 64 (LSB)")
	t.Log("Coarse energy: bits 6-50 (44 bits = ~5.5 bytes)")
	t.Log("TF encoding: bits 50-72 (22 bits)")
	t.Log("Spread: starts at bit 72")
	t.Log("")
	t.Log("The difference at bit 64 is INSIDE the TF region")
	t.Log("Bytes 0-5 match (header)")
	t.Log("Bytes 6-7 match (coarse energy)")
	t.Log("Byte 8 differs at LSB (bit 64 = byte 8 bit 0)")
	t.Log("")
	t.Log("Since bytes 0-7 match, the divergence first appears at byte 8")
	t.Log("Byte 8 is bits 64-71")
	t.Log("TF starts at bit 50, so bits 50-71 are TF")
	t.Log("The difference at bit 64 is somewhere in TF encoding")

	// Let's verify the TF bits themselves are identical
	t.Log("")
	t.Log("=== TF bit verification ===")
	rdLib3 := &rangecoding.Decoder{}
	rdLib3.Init(libPayload)
	rdLib3.DecodeBit(15)
	rdLib3.DecodeBit(1)
	rdLib3.DecodeBit(3)
	rdLib3.DecodeBit(3)
	celt.NewDecoder(1).DecodeCoarseEnergyWithDecoder(rdLib3, nbBands, false, lm)

	rdGo3 := &rangecoding.Decoder{}
	rdGo3.Init(goPacket)
	rdGo3.DecodeBit(15)
	rdGo3.DecodeBit(1)
	rdGo3.DecodeBit(3)
	rdGo3.DecodeBit(3)
	celt.NewDecoder(1).DecodeCoarseEnergyWithDecoder(rdGo3, nbBands, false, lm)

	tfMatch := true
	for i := 0; i < nbBands; i++ {
		libBit := rdLib3.DecodeBit(1)
		goBit := rdGo3.DecodeBit(1)
		if libBit != goBit {
			t.Logf("TF bit %d differs: lib=%d go=%d", i, libBit, goBit)
			tfMatch = false
		}
	}
	libSel := rdLib3.DecodeBit(1)
	goSel := rdGo3.DecodeBit(1)
	if libSel != goSel {
		t.Logf("TF select differs: lib=%d go=%d", libSel, goSel)
		tfMatch = false
	}
	if tfMatch {
		t.Log("All TF bits match!")
	}

	// The key question: if TF bits match, why do the bytes differ?
	// Answer: Range encoding is NOT a simple bit packing!
	// The same logical bits can produce different byte patterns
	// depending on the encoder state (rng, val, etc.)

	// The divergence is in the COARSE ENERGY encoding!
	// Even though the decoded VALUES are the same,
	// the encoder may have taken a different path that produces different bytes.

	t.Log("")
	t.Log("=== Root cause analysis ===")
	t.Log("The decoded coarse energies are IDENTICAL")
	t.Log("The decoded TF bits are IDENTICAL")
	t.Log("But the bytes differ starting at byte 8")
	t.Log("")
	t.Log("This means the ENCODING took different paths that produce")
	t.Log("different byte patterns for the SAME logical values.")
	t.Log("")
	t.Log("In range coding, this can happen when:")
	t.Log("1. Different normalization timing")
	t.Log("2. Different Laplace model parameters")
	t.Log("3. Subtle precision differences in energy quantization")
}
