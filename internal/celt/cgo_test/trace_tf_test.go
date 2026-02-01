//go:build trace
// +build trace

// Package cgo traces TF encoding differences between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceTFDivergence traces the exact TF encoding difference.
func TestTraceTFDivergence(t *testing.T) {
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

	t.Log("=== Decoding TF from LIBOPUS packet ===")
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	// Decode header
	silenceLib := rdLib.DecodeBit(15)
	postfilterLib := rdLib.DecodeBit(1)
	transientLib := rdLib.DecodeBit(3)
	intraLib := rdLib.DecodeBit(3)
	t.Logf("Header: silence=%d postfilter=%d transient=%d intra=%d (tell=%d)",
		silenceLib, postfilterLib, transientLib, intraLib, rdLib.Tell())

	// Decode coarse energy
	goDecLib := celt.NewDecoder(1)
	coarseLib := goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, intraLib == 1, lm)
	t.Logf("Tell after coarse: %d bits", rdLib.Tell())
	t.Logf("Coarse[0..5]: %.2f, %.2f, %.2f, %.2f, %.2f, %.2f",
		coarseLib[0], coarseLib[1], coarseLib[2], coarseLib[3], coarseLib[4], coarseLib[5])

	// Now decode TF step by step
	t.Log("")
	t.Log("=== TF Decoding (LIBOPUS) ===")
	tellBeforeTFLib := rdLib.Tell()
	t.Logf("Tell before TF: %d", tellBeforeTFLib)

	// TF decoding for transient mode uses tf_select_table differently
	// The bits are CODED with differential encoding
	isTransientLib := transientLib == 1

	// Decode using the exported test function
	tfResLib := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, isTransientLib, tfResLib, lm, rdLib)
	tellAfterTFLib := rdLib.Tell()
	t.Logf("Tell after TF: %d (used %d bits)", tellAfterTFLib, tellAfterTFLib-tellBeforeTFLib)
	t.Logf("TF results: %v", tfResLib[:10])

	t.Log("")
	t.Log("=== Decoding TF from GOPUS packet ===")
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

	// Decode header
	silenceGo := rdGo.DecodeBit(15)
	postfilterGo := rdGo.DecodeBit(1)
	transientGo := rdGo.DecodeBit(3)
	intraGo := rdGo.DecodeBit(3)
	t.Logf("Header: silence=%d postfilter=%d transient=%d intra=%d (tell=%d)",
		silenceGo, postfilterGo, transientGo, intraGo, rdGo.Tell())

	// Decode coarse energy
	goDecGo := celt.NewDecoder(1)
	coarseGo := goDecGo.DecodeCoarseEnergyWithDecoder(rdGo, nbBands, intraGo == 1, lm)
	t.Logf("Tell after coarse: %d bits", rdGo.Tell())
	t.Logf("Coarse[0..5]: %.2f, %.2f, %.2f, %.2f, %.2f, %.2f",
		coarseGo[0], coarseGo[1], coarseGo[2], coarseGo[3], coarseGo[4], coarseGo[5])

	// Now decode TF step by step
	t.Log("")
	t.Log("=== TF Decoding (GOPUS) ===")
	tellBeforeTFGo := rdGo.Tell()
	t.Logf("Tell before TF: %d", tellBeforeTFGo)

	isTransientGo := transientGo == 1
	tfResGo := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, isTransientGo, tfResGo, lm, rdGo)
	tellAfterTFGo := rdGo.Tell()
	t.Logf("Tell after TF: %d (used %d bits)", tellAfterTFGo, tellAfterTFGo-tellBeforeTFGo)
	t.Logf("TF results: %v", tfResGo[:10])

	// Compare
	t.Log("")
	t.Log("=== TF Comparison ===")
	t.Logf("Transient: lib=%v go=%v", isTransientLib, isTransientGo)
	t.Logf("Bits used for TF: lib=%d go=%d", tellAfterTFLib-tellBeforeTFLib, tellAfterTFGo-tellBeforeTFGo)

	tfDiff := false
	for i := 0; i < nbBands; i++ {
		if tfResLib[i] != tfResGo[i] {
			t.Logf("TF diff at band %d: lib=%d go=%d", i, tfResLib[i], tfResGo[i])
			tfDiff = true
		}
	}
	if !tfDiff {
		t.Log("TF values match!")
	}

	// Now let's look at the raw bits before TF decoding
	t.Log("")
	t.Log("=== Raw bits around coarse/TF boundary ===")
	// Bytes 5-8 contain the TF info
	for i := 5; i <= 10; i++ {
		if i < len(libPayload) && i < len(goPacket) {
			marker := ""
			if libPayload[i] != goPacket[i] {
				marker = " <-- DIFFERS"
			}
			t.Logf("Byte %d: lib=0x%02X (%08b) go=0x%02X (%08b)%s",
				i, libPayload[i], libPayload[i], goPacket[i], goPacket[i], marker)
		}
	}

	// Detailed bit-by-bit TF decode
	t.Log("")
	t.Log("=== Bit-by-bit TF decode from LIBOPUS ===")
	rdLib2 := &rangecoding.Decoder{}
	rdLib2.Init(libPayload)
	rdLib2.DecodeBit(15) // silence
	rdLib2.DecodeBit(1)  // postfilter
	rdLib2.DecodeBit(3)  // transient
	rdLib2.DecodeBit(3)  // intra
	celt.NewDecoder(1).DecodeCoarseEnergyWithDecoder(rdLib2, nbBands, intraLib == 1, lm)

	t.Logf("Tell before TF bits: %d", rdLib2.Tell())

	// Decode raw TF bits (not using the select table yet)
	tfBitsLib := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		tfBitsLib[i] = rdLib2.DecodeBit(1)
	}
	tfSelectLib := rdLib2.DecodeBit(1)
	t.Logf("Raw TF bits: %v", tfBitsLib[:10])
	t.Logf("TF select: %d", tfSelectLib)
	t.Logf("Tell after raw TF: %d", rdLib2.Tell())

	t.Log("")
	t.Log("=== Bit-by-bit TF decode from GOPUS ===")
	rdGo2 := &rangecoding.Decoder{}
	rdGo2.Init(goPacket)
	rdGo2.DecodeBit(15) // silence
	rdGo2.DecodeBit(1)  // postfilter
	rdGo2.DecodeBit(3)  // transient
	rdGo2.DecodeBit(3)  // intra
	celt.NewDecoder(1).DecodeCoarseEnergyWithDecoder(rdGo2, nbBands, intraGo == 1, lm)

	t.Logf("Tell before TF bits: %d", rdGo2.Tell())

	// Decode raw TF bits
	tfBitsGo := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		tfBitsGo[i] = rdGo2.DecodeBit(1)
	}
	tfSelectGo := rdGo2.DecodeBit(1)
	t.Logf("Raw TF bits: %v", tfBitsGo[:10])
	t.Logf("TF select: %d", tfSelectGo)
	t.Logf("Tell after raw TF: %d", rdGo2.Tell())

	// Compare raw TF bits
	t.Log("")
	t.Log("=== Raw TF bit comparison ===")
	for i := 0; i < nbBands; i++ {
		if tfBitsLib[i] != tfBitsGo[i] {
			t.Logf("Raw TF bit diff at band %d: lib=%d go=%d", i, tfBitsLib[i], tfBitsGo[i])
		}
	}
	if tfSelectLib != tfSelectGo {
		t.Logf("TF select diff: lib=%d go=%d", tfSelectLib, tfSelectGo)
	}
}
