//go:build trace
// +build trace

// Package cgo traces dynalloc encoding differences between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceDynallocDivergence traces the exact dynalloc encoding difference.
func TestTraceDynallocDivergence(t *testing.T) {
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
	targetBytes := 159
	targetBits := targetBytes * 8
	bitRes := 3
	totalBitsQ3ForDynalloc := targetBits << bitRes

	t.Log("=== Decoding dynalloc from LIBOPUS packet ===")
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	// Skip header
	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	transientLib := rdLib.DecodeBit(3)
	intraLib := rdLib.DecodeBit(3)

	// Decode coarse energy
	goDecLib := celt.NewDecoder(1)
	goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, intraLib == 1, lm)

	// Decode TF
	for i := 0; i < nbBands; i++ {
		rdLib.DecodeBit(1)
	}
	rdLib.DecodeBit(1) // tf_select

	// Decode spread
	spreadICDF := []uint8{25, 23, 2, 0}
	rdLib.DecodeICDF(spreadICDF, 5)

	tellBeforeDynallocLib := rdLib.Tell()
	t.Logf("Tell before dynalloc: %d bits", tellBeforeDynallocLib)

	// Decode dynalloc, tracking each step
	capsLib := celt.InitCaps(nbBands, lm, 1)
	offsetsLib := make([]int, nbBands)
	dynallocLogpLib := 6
	totalBoostLib := 0
	tellFracDynallocLib := rdLib.TellFrac()
	bitsEncodedLib := 0

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

		dynallocLoopLogp := dynallocLogpLib
		boost := 0
		bitsThisBand := 0

		for j := 0; tellFracDynallocLib+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoostLib && boost < capsLib[i]; j++ {
			tellBefore := rdLib.TellFrac()
			flag := rdLib.DecodeBit(uint(dynallocLoopLogp))
			tellAfter := rdLib.TellFrac()
			bitsUsed := (tellAfter - tellBefore) >> bitRes // Convert Q3 to bits
			bitsThisBand += bitsUsed

			tellFracDynallocLib = rdLib.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoostLib += quanta
			dynallocLoopLogp = 1
		}

		if boost > 0 && dynallocLogpLib > 2 {
			dynallocLogpLib--
		}
		offsetsLib[i] = boost
		bitsEncodedLib += bitsThisBand

		if boost > 0 || bitsThisBand > 0 {
			t.Logf("LIB Band %2d: boost=%d, bits_used=%d, offset=%d, cap=%d", i, boost, bitsThisBand, boost, capsLib[i])
		}
	}
	tellAfterDynallocLib := rdLib.Tell()
	t.Logf("LIB Total dynalloc bits: %d (tell after: %d)", tellAfterDynallocLib-tellBeforeDynallocLib, tellAfterDynallocLib)

	t.Log("")
	t.Log("=== Decoding dynalloc from GOPUS packet ===")
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

	// Skip header
	rdGo.DecodeBit(15) // silence
	rdGo.DecodeBit(1)  // postfilter
	transientGo := rdGo.DecodeBit(3)
	intraGo := rdGo.DecodeBit(3)

	// Decode coarse energy
	goDecGo := celt.NewDecoder(1)
	goDecGo.DecodeCoarseEnergyWithDecoder(rdGo, nbBands, intraGo == 1, lm)

	// Decode TF
	for i := 0; i < nbBands; i++ {
		rdGo.DecodeBit(1)
	}
	rdGo.DecodeBit(1) // tf_select

	// Decode spread
	rdGo.DecodeICDF(spreadICDF, 5)

	tellBeforeDynallocGo := rdGo.Tell()
	t.Logf("Tell before dynalloc: %d bits", tellBeforeDynallocGo)

	// Decode dynalloc
	capsGo := celt.InitCaps(nbBands, lm, 1)
	offsetsGo := make([]int, nbBands)
	dynallocLogpGo := 6
	totalBoostGo := 0
	tellFracDynallocGo := rdGo.TellFrac()
	bitsEncodedGo := 0

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

		dynallocLoopLogp := dynallocLogpGo
		boost := 0
		bitsThisBand := 0

		for j := 0; tellFracDynallocGo+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoostGo && boost < capsGo[i]; j++ {
			tellBefore := rdGo.TellFrac()
			flag := rdGo.DecodeBit(uint(dynallocLoopLogp))
			tellAfter := rdGo.TellFrac()
			bitsUsed := (tellAfter - tellBefore) >> bitRes
			bitsThisBand += bitsUsed

			tellFracDynallocGo = rdGo.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoostGo += quanta
			dynallocLoopLogp = 1
		}

		if boost > 0 && dynallocLogpGo > 2 {
			dynallocLogpGo--
		}
		offsetsGo[i] = boost
		bitsEncodedGo += bitsThisBand

		if boost > 0 || bitsThisBand > 0 {
			t.Logf("GO  Band %2d: boost=%d, bits_used=%d, offset=%d, cap=%d", i, boost, bitsThisBand, boost, capsGo[i])
		}
	}
	tellAfterDynallocGo := rdGo.Tell()
	t.Logf("GO  Total dynalloc bits: %d (tell after: %d)", tellAfterDynallocGo-tellBeforeDynallocGo, tellAfterDynallocGo)

	// Compare
	t.Log("")
	t.Log("=== Comparison ===")
	t.Logf("Transient: lib=%d go=%d", transientLib, transientGo)
	t.Logf("Intra: lib=%d go=%d", intraLib, intraGo)
	t.Logf("Tell before dynalloc: lib=%d go=%d", tellBeforeDynallocLib, tellBeforeDynallocGo)
	t.Logf("Tell after dynalloc: lib=%d go=%d (diff=%d)", tellAfterDynallocLib, tellAfterDynallocGo, tellAfterDynallocLib-tellAfterDynallocGo)
	t.Logf("Total boost: lib=%d go=%d", totalBoostLib, totalBoostGo)

	// Show offset differences
	for i := 0; i < nbBands; i++ {
		if offsetsLib[i] != offsetsGo[i] {
			t.Logf("Offset diff at band %d: lib=%d go=%d", i, offsetsLib[i], offsetsGo[i])
		}
	}

	// Now let's look at what gopus computes for dynalloc offsets BEFORE encoding
	t.Log("")
	t.Log("=== gopus computed dynalloc offsets ===")

	// Re-encode with gopus to get dynalloc result
	goEnc2 := celt.NewEncoder(1)
	goEnc2.Reset()
	goEnc2.SetBitrate(bitrate)
	goEnc2.SetComplexity(10)
	goEnc2.SetVBR(false)

	// Enable dynalloc debug
	goEnc2.EncodeFrame(pcm64, frameSize)
	dynalloc := goEnc2.GetLastDynalloc()

	t.Logf("Gopus computed offsets: %v", dynalloc.Offsets[:nbBands])
	t.Logf("Gopus TotBoost: %d", dynalloc.TotBoost)

	// The key question: are the COMPUTED offsets the same as what libopus DECODES?
	// If not, the issue is in DynallocAnalysis
	// If yes, the issue is in the encoding of those offsets

	offsetsFromLib := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		// Convert from boost (quanta units) to offset count
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
		if quanta > 0 && offsetsLib[i] > 0 {
			offsetsFromLib[i] = offsetsLib[i] / quanta
		}
	}

	t.Logf("Lib offsets (count form): %v", offsetsFromLib)

	// Convert gopus offsets to same form
	offsetsFromGo := make([]int, nbBands)
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
		if quanta > 0 && offsetsGo[i] > 0 {
			offsetsFromGo[i] = offsetsGo[i] / quanta
		}
	}

	t.Logf("Go  offsets (count form): %v", offsetsFromGo)
}

// TestCompareDynallocOffsets compares the dynalloc offset computation directly.
func TestCompareDynallocOffsets(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	// Get gopus dynalloc result
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goEnc.EncodeFrame(pcm64, frameSize)
	dynalloc := goEnc.GetLastDynalloc()

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	t.Logf("Gopus dynalloc offsets: %v", dynalloc.Offsets[:nbBands])
	t.Logf("Gopus MaxDepth: %.4f", dynalloc.MaxDepth)
	t.Logf("Gopus TotBoost: %d", dynalloc.TotBoost)
	t.Logf("Gopus Importance: %v", dynalloc.Importance[:nbBands])

	// The issue might be that gopus computes offsets=0 for all bands,
	// but libopus actually encodes some boost bits.
	// This could mean:
	// 1. DynallocAnalysis is wrong
	// 2. The encoding loop is not encoding the same offsets

	// Let's also check what the bit budget looks like
	effectiveBytes := 159
	minBytes := 30 + 5*mode.LM
	t.Logf("effectiveBytes=%d, minBytes=%d, lm=%d", effectiveBytes, minBytes, mode.LM)
	t.Logf("effectiveBytes >= minBytes: %v", effectiveBytes >= minBytes)
}
