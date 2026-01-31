// Package cgo traces real encoding from both encoders.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceRealEncodingDivergence traces the actual encoding from both encoders.
func TestTraceRealEncodingDivergence(t *testing.T) {
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

	t.Logf("gopus packet: %d bytes", len(goPacket))
	t.Logf("libopus payload: %d bytes", len(libPayload))

	// Find first divergence
	minLen := len(goPacket)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	firstDiff := -1
	for i := 0; i < minLen; i++ {
		if goPacket[i] != libPayload[i] {
			firstDiff = i
			break
		}
	}

	if firstDiff < 0 {
		t.Log("Packets match completely!")
		return
	}

	t.Logf("First divergence at byte %d (bit %d)", firstDiff, firstDiff*8)

	// Now decode both packets up to the divergence point
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	targetBytes := 159
	targetBits := targetBytes * 8

	t.Log("")
	t.Log("=== Decoding LIBOPUS packet ===")
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

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

	// Decode TF
	tfResLib := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		tfResLib[i] = rdLib.DecodeBit(1)
	}
	tfSelectLib := rdLib.DecodeBit(1)
	t.Logf("Tell after TF: %d bits", rdLib.Tell())

	// Decode spread
	spreadICDF := []uint8{25, 23, 2, 0}
	spreadLib := rdLib.DecodeICDF(spreadICDF, 5)
	t.Logf("Spread=%d, Tell after spread: %d bits", spreadLib, rdLib.Tell())

	// Decode dynalloc
	bitRes := 3
	capsLib := celt.InitCaps(nbBands, lm, 1)
	offsetsLib := make([]int, nbBands)
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

		for j := 0; tellFracDynalloc+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoost && boost < capsLib[i]; j++ {
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
		offsetsLib[i] = boost
	}
	t.Logf("Tell after dynalloc: %d bits", rdLib.Tell())

	// Decode trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	trimLib := rdLib.DecodeICDF(trimICDF, 7)
	t.Logf("Trim=%d, Tell after trim: %d bits", trimLib, rdLib.Tell())

	// Compute allocation
	bitsUsedLib := rdLib.TellFrac()
	totalBitsQ3Lib := (targetBits << bitRes) - bitsUsedLib - 1
	antiCollapseRsv := 1 << bitRes
	totalBitsQ3Lib -= antiCollapseRsv

	allocResultLib := celt.ComputeAllocationWithDecoder(
		rdLib, totalBitsQ3Lib>>bitRes,
		nbBands, 1, capsLib, offsetsLib, trimLib,
		nbBands, false, lm,
	)
	t.Logf("Tell after allocation: %d bits", rdLib.Tell())

	// Decode fine energy
	fineQLib := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		fineBits := allocResultLib.FineBits[i]
		if fineBits == 0 {
			continue
		}
		q := rdLib.DecodeUniform(uint32(1 << uint(fineBits)))
		fineQLib[i] = int(q)
	}
	t.Logf("Tell after fine energy: %d bits", rdLib.Tell())

	t.Log("")
	t.Log("=== Decoding GOPUS packet ===")
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

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

	// Decode TF
	tfResGo := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		tfResGo[i] = rdGo.DecodeBit(1)
	}
	tfSelectGo := rdGo.DecodeBit(1)
	t.Logf("Tell after TF: %d bits", rdGo.Tell())

	// Decode spread
	spreadGo := rdGo.DecodeICDF(spreadICDF, 5)
	t.Logf("Spread=%d, Tell after spread: %d bits", spreadGo, rdGo.Tell())

	// Decode dynalloc
	capsGo := celt.InitCaps(nbBands, lm, 1)
	offsetsGo := make([]int, nbBands)
	dynallocLogpGo := 6
	totalBoostGo := 0
	tellFracDynallocGo := rdGo.TellFrac()

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

		for j := 0; tellFracDynallocGo+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoostGo && boost < capsGo[i]; j++ {
			flag := rdGo.DecodeBit(uint(dynallocLoopLogp))
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
	}
	t.Logf("Tell after dynalloc: %d bits", rdGo.Tell())

	// Decode trim
	trimGo := rdGo.DecodeICDF(trimICDF, 7)
	t.Logf("Trim=%d, Tell after trim: %d bits", trimGo, rdGo.Tell())

	// Compute allocation
	bitsUsedGo := rdGo.TellFrac()
	totalBitsQ3Go := (targetBits << bitRes) - bitsUsedGo - 1
	totalBitsQ3Go -= antiCollapseRsv

	allocResultGo := celt.ComputeAllocationWithDecoder(
		rdGo, totalBitsQ3Go>>bitRes,
		nbBands, 1, capsGo, offsetsGo, trimGo,
		nbBands, false, lm,
	)
	t.Logf("Tell after allocation: %d bits", rdGo.Tell())

	// Decode fine energy
	fineQGo := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		fineBits := allocResultGo.FineBits[i]
		if fineBits == 0 {
			continue
		}
		q := rdGo.DecodeUniform(uint32(1 << uint(fineBits)))
		fineQGo[i] = int(q)
	}
	t.Logf("Tell after fine energy: %d bits", rdGo.Tell())

	// Now compare
	t.Log("")
	t.Log("=== Coarse Energy Comparison ===")
	coarseDiffers := false
	for i := 0; i < nbBands; i++ {
		diff := math.Abs(coarseLib[i] - coarseGo[i])
		marker := ""
		if diff > 0.001 {
			marker = " <-- DIFF"
			coarseDiffers = true
		}
		t.Logf("Band %2d: lib=%+.4f go=%+.4f diff=%+.6f%s",
			i, coarseLib[i], coarseGo[i], coarseLib[i]-coarseGo[i], marker)
	}

	if coarseDiffers {
		t.Log("")
		t.Log("COARSE ENERGIES DIFFER - this is the root cause!")
	}

	t.Log("")
	t.Log("=== Fine Quant Comparison ===")
	fineQDiffers := false
	for i := 0; i < nbBands; i++ {
		fbLib := allocResultLib.FineBits[i]
		fbGo := allocResultGo.FineBits[i]
		if fbLib == 0 && fbGo == 0 {
			continue
		}
		marker := ""
		if fineQLib[i] != fineQGo[i] || fbLib != fbGo {
			marker = " <-- DIFF"
			fineQDiffers = true
		}
		t.Logf("Band %2d: lib_q=%d go_q=%d (lib_fb=%d go_fb=%d)%s",
			i, fineQLib[i], fineQGo[i], fbLib, fbGo, marker)
	}

	if fineQDiffers {
		t.Log("")
		t.Log("FINE QUANT DIFFERS!")
	}

	// TF comparison
	t.Log("")
	t.Log("=== TF Comparison ===")
	tfDiffers := false
	for i := 0; i < nbBands; i++ {
		if tfResLib[i] != tfResGo[i] {
			t.Logf("TF band %d: lib=%d go=%d <-- DIFF", i, tfResLib[i], tfResGo[i])
			tfDiffers = true
		}
	}
	if !tfDiffers {
		t.Log("TF values match")
	}
	t.Logf("TF select: lib=%d go=%d", tfSelectLib, tfSelectGo)

	// Other parameters
	t.Log("")
	t.Log("=== Other Parameters ===")
	t.Logf("Spread: lib=%d go=%d", spreadLib, spreadGo)
	t.Logf("Trim: lib=%d go=%d", trimLib, trimGo)

	// Final range
	t.Log("")
	t.Logf("Final range: gopus=0x%08X libopus=0x%08X", goEnc.FinalRange(), libEnc.GetFinalRange())
}
