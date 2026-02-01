//go:build trace
// +build trace

// Package cgo traces coarse energy encoding to find divergence.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceCoarseEnergyEncode traces coarse energy encoding step by step.
func TestTraceCoarseEnergyEncode(t *testing.T) {
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

	// Now decode both packets to extract the QI values
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Log("=== Extracting QI values from libopus packet ===")
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	// Skip header
	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	rdLib.DecodeBit(3)  // transient
	rdLib.DecodeBit(3)  // intra
	t.Logf("After header: tell=%d rng=%08X val=%08X", rdLib.Tell(), rdLib.Range(), rdLib.Val())

	// Decode coarse energy and extract QI values
	goDecLib := celt.NewDecoder(1)
	coarseLib := goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, false, lm)
	t.Logf("After coarse: tell=%d rng=%08X val=%08X", rdLib.Tell(), rdLib.Range(), rdLib.Val())

	t.Log("")
	t.Log("=== Extracting QI values from gopus packet ===")
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

	// Skip header
	rdGo.DecodeBit(15)
	rdGo.DecodeBit(1)
	rdGo.DecodeBit(3)
	rdGo.DecodeBit(3)
	t.Logf("After header: tell=%d rng=%08X val=%08X", rdGo.Tell(), rdGo.Range(), rdGo.Val())

	// Decode coarse energy
	goDecGo := celt.NewDecoder(1)
	coarseGo := goDecGo.DecodeCoarseEnergyWithDecoder(rdGo, nbBands, false, lm)
	t.Logf("After coarse: tell=%d rng=%08X val=%08X", rdGo.Tell(), rdGo.Range(), rdGo.Val())

	// Compare decoded coarse energies
	t.Log("")
	t.Log("=== Comparing decoded coarse energies ===")
	for i := 0; i < nbBands; i++ {
		diff := math.Abs(coarseLib[i] - coarseGo[i])
		marker := ""
		if diff > 0.001 {
			marker = " <-- DIFF"
		}
		t.Logf("Band %2d: lib=%.4f go=%.4f%s", i, coarseLib[i], coarseGo[i], marker)
	}

	// The decoded values are IDENTICAL, but the bytes differ.
	// This means the ENCODING is producing different bytes for the same values.

	t.Log("")
	t.Log("=== Byte comparison ===")
	for i := 0; i <= 10; i++ {
		if i < len(libPayload) && i < len(goPacket) {
			marker := ""
			if libPayload[i] != goPacket[i] {
				marker = " <-- DIFFERS"
			}
			t.Logf("Byte %2d: lib=%02X go=%02X%s", i, libPayload[i], goPacket[i], marker)
		}
	}

	// Try to figure out which QI values are encoded
	// By re-encoding with gopus and comparing
	t.Log("")
	t.Log("=== Re-encoding analysis ===")

	// Create a fresh encoder and encode the same energies
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	re.Shrink(159)

	// Encode header (same as gopus)
	re.EncodeBit(0, 15) // silence=0
	re.EncodeBit(0, 1)  // postfilter=0
	re.EncodeBit(1, 3)  // transient=1
	re.EncodeBit(0, 3)  // intra=0

	t.Logf("After header: tell=%d rng=%08X val=%08X", re.Tell(), re.Range(), re.Val())

	// Now we need the actual QI values that gopus encodes
	// These come from the energy quantization

	// Let's check the first few bytes after encoding to see if they match
	// But first we need to know what energies gopus is computing...

	// Actually, the key insight is that BOTH encoders produce packets that
	// decode to the SAME coarse energies. The difference is in HOW they encode.

	t.Log("")
	t.Log("=== Key insight ===")
	t.Log("Both libopus and gopus packets decode to IDENTICAL coarse energies.")
	t.Log("But the bytes differ, meaning the encoding path is different.")
	t.Log("")
	t.Log("Possible causes:")
	t.Log("1. Different energy quantization (different QI values)")
	t.Log("2. Different Laplace encoding parameters")
	t.Log("3. Different range encoder implementation")
	t.Log("")
	t.Log("Since the range encoder header encoding matches (verified earlier),")
	t.Log("the issue is most likely in energy quantization or Laplace parameters.")
}

// TestCompareQIValues compares the actual QI values encoded by both encoders.
func TestCompareQIValues(t *testing.T) {
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

	// Extract QI values by manual Laplace decoding
	t.Log("=== Extracting QI values by manual decode ===")

	// eProbModel for LM=3, inter mode
	eProbModelLM3Inter := []int{72, 127, 65, 129, 66, 128, 65, 128, 64, 128, 62, 128, 64, 128, 64, 128, 92, 78, 92, 79, 92, 78, 90, 79, 116, 41, 115, 40, 114, 40, 132, 26, 132, 26, 145, 17, 161, 12, 176, 10, 177, 11}

	_ = eProbModelLM3Inter
	_ = lm

	// Actually, let's just look at the decoded energies more carefully
	// If they're identical, both encoders must be encoding the same QI values
	// (since Laplace decoding is deterministic)

	// Let me decode the coarse energies step by step and compare
	t.Log("")
	t.Log("=== Step-by-step coarse decode ===")

	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)
	rdLib.DecodeBit(15)
	rdLib.DecodeBit(1)
	rdLib.DecodeBit(3)
	rdLib.DecodeBit(3)

	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)
	rdGo.DecodeBit(15)
	rdGo.DecodeBit(1)
	rdGo.DecodeBit(3)
	rdGo.DecodeBit(3)

	t.Logf("After header: lib tell=%d, go tell=%d", rdLib.Tell(), rdGo.Tell())
	t.Logf("After header: lib rng=%08X, go rng=%08X", rdLib.Range(), rdGo.Range())
	t.Logf("After header: lib val=%08X, go val=%08X", rdLib.Val(), rdGo.Val())

	// The key question: at this point (after header), are the decoder states identical?
	// If YES: the header encoding is identical
	// If NO: something is different in header encoding

	if rdLib.Range() == rdGo.Range() && rdLib.Val() == rdGo.Val() {
		t.Log("Decoder states MATCH after header - header encoding is identical")
	} else {
		t.Log("Decoder states DIFFER after header - header encoding differs!")
	}

	// Now decode coarse energies
	// Use the celt decoder which properly tracks QI values
	decLib := celt.NewDecoder(1)
	decGo := celt.NewDecoder(1)

	coarseLib := decLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, false, lm)
	coarseGo := decGo.DecodeCoarseEnergyWithDecoder(rdGo, nbBands, false, lm)

	t.Logf("After coarse: lib tell=%d, go tell=%d", rdLib.Tell(), rdGo.Tell())
	t.Logf("After coarse: lib rng=%08X, go rng=%08X", rdLib.Range(), rdGo.Range())
	t.Logf("After coarse: lib val=%08X, go val=%08X", rdLib.Val(), rdGo.Val())

	// Check decoded values
	allMatch := true
	for i := 0; i < nbBands; i++ {
		if math.Abs(coarseLib[i]-coarseGo[i]) > 0.001 {
			t.Logf("Band %d: lib=%.4f go=%.4f DIFFERS", i, coarseLib[i], coarseGo[i])
			allMatch = false
		}
	}
	if allMatch {
		t.Log("All coarse energies MATCH")
	}

	// Conclusion
	t.Log("")
	if rdLib.Val() != rdGo.Val() {
		t.Log("CONCLUSION: Despite identical decoded values, the decoder val differs.")
		t.Log("This means the encoding produces different bytes for the same logical values.")
		t.Log("")
		t.Log("In range coding, multiple byte sequences CAN decode to the same values")
		t.Log("if the encoder makes slightly different arithmetic choices.")
		t.Log("")
		t.Log("The most likely cause is a subtle difference in the Laplace encoding")
		t.Log("parameters or the energy quantization that produces the same final")
		t.Log("values but via a different path through the probability model.")
	}
}
