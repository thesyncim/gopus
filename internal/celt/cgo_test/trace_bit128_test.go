//go:build trace
// +build trace

// Package cgo traces exactly which band causes the bit 128 divergence.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceBit128Divergence traces which band causes the divergence at bit 128.
func TestTraceBit128Divergence(t *testing.T) {
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

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	targetBytes := 159
	targetBits := targetBytes * 8

	// Decode libopus to get allocation and fine bits
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	// Skip header
	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	rdLib.DecodeBit(3)  // transient
	rdLib.DecodeBit(3)  // intra

	// Decode coarse
	goDecLib := celt.NewDecoder(1)
	goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, false, lm)

	// Skip TF
	for i := 0; i < nbBands; i++ {
		rdLib.DecodeBit(1)
	}
	rdLib.DecodeBit(1)

	// Skip spread
	spreadICDF := []uint8{25, 23, 2, 0}
	rdLib.DecodeICDF(spreadICDF, 5)

	// Decode dynalloc
	bitRes := 3
	caps := celt.InitCaps(nbBands, lm, 1)
	offsets := make([]int, nbBands)
	totalBitsQ3ForDynalloc := targetBits << bitRes
	dynallocLogp := 6
	totalBoost := 0
	tellFracDynalloc := rdLib.TellFrac()

	for i := 0; i < nbBands; i++ {
		width := celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := 6 << bitRes
		if width > innerMax {
			innerMax = width
		}
		quanta := width << bitRes
		if quanta > innerMax {
			quanta = innerMax
		}

		dynallocLoopLogp := dynallocLogp
		boost := 0

		for j := 0; tellFracDynalloc+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoost && boost < caps[i]; j++ {
			flag := rdLib.DecodeBit(uint(dynallocLoopLogp))
			tellFracDynalloc = rdLib.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoost += quanta
			dynallocLoopLogp = 1
		}

		if boost > 0 && dynallocLogp > 2 {
			dynallocLogp--
		}
		offsets[i] = boost
	}

	// Decode trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	trim := rdLib.DecodeICDF(trimICDF, 7)

	// Compute allocation
	bitsUsed := rdLib.TellFrac()
	totalBitsQ3 := (targetBits << bitRes) - bitsUsed - 1
	antiCollapseRsv := 1 << bitRes
	totalBitsQ3 -= antiCollapseRsv

	allocResult := celt.ComputeAllocationWithDecoder(
		rdLib, totalBitsQ3>>bitRes,
		nbBands, 1, caps, offsets, trim,
		nbBands, false, lm,
	)

	fineEnergyStartBit := rdLib.Tell()
	t.Logf("Fine energy starts at bit %d", fineEnergyStartBit)
	t.Logf("Divergence at bit 128, which is bit %d into fine energy", 128-fineEnergyStartBit)

	// Calculate which band contains bit 128
	t.Log("")
	t.Log("Band | fineBits | startBit | endBit | contains bit 128?")
	t.Log("-----+----------+----------+--------+------------------")

	cumulativeBits := fineEnergyStartBit
	for i := 0; i < nbBands; i++ {
		fb := allocResult.FineBits[i]
		if fb == 0 {
			continue
		}
		startBit := cumulativeBits
		endBit := cumulativeBits + fb
		contains := ""
		if startBit <= 128 && 128 < endBit {
			contains = "<-- CONTAINS BIT 128"
		}
		t.Logf("  %2d |    %d     |   %3d    |  %3d   | %s", i, fb, startBit, endBit, contains)
		cumulativeBits = endBit
	}

	t.Logf("Fine energy ends at bit %d", cumulativeBits)

	// Now trace the actual encoding bit by bit around the divergence
	t.Log("")
	t.Log("=== Byte-level analysis around divergence ===")

	// Encode with gopus and trace
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)

	t.Logf("Byte 15: gopus=0x%02x libopus=0x%02x", goPacket[15], libPayload[15])
	t.Logf("Byte 16: gopus=0x%02x libopus=0x%02x XOR=0x%02x", goPacket[16], libPayload[16], goPacket[16]^libPayload[16])
	t.Logf("Byte 17: gopus=0x%02x libopus=0x%02x XOR=0x%02x", goPacket[17], libPayload[17], goPacket[17]^libPayload[17])

	// The XOR pattern shows where divergence propagates
	t.Log("")
	t.Log("Binary comparison:")
	t.Logf("Byte 16 gopus:   %08b", goPacket[16])
	t.Logf("Byte 16 libopus: %08b", libPayload[16])
	t.Logf("XOR:             %08b (first differing bit: %d)", goPacket[16]^libPayload[16], findFirstSetBit(goPacket[16]^libPayload[16]))
}

func findFirstSetBit(b byte) int {
	for i := 0; i < 8; i++ {
		if (b>>i)&1 == 1 {
			return i
		}
	}
	return -1
}
