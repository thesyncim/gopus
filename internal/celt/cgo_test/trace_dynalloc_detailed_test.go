// Package cgo traces dynalloc encoding step-by-step.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceDynallocStepByStep traces dynalloc encoding/decoding step-by-step.
func TestTraceDynallocStepByStep(t *testing.T) {
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
	targetBytes := 159
	targetBits := targetBytes * 8
	bitRes := 3
	totalBitsQ3ForDynalloc := targetBits << bitRes

	// === LIBOPUS ===
	t.Log("=== LIBOPUS dynalloc decoding ===")
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	// Header
	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	transientLib := rdLib.DecodeBit(3)
	rdLib.DecodeBit(3) // intra

	// Coarse energy
	goDecLib := celt.NewDecoder(1)
	goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, false, lm)

	// TF
	tfResLib := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, transientLib == 1, tfResLib, lm, rdLib)

	// Spread
	spreadICDF := []uint8{25, 23, 2, 0}
	rdLib.DecodeICDF(spreadICDF, 5)

	tellBeforeLib := rdLib.Tell()
	tellFracBeforeLib := rdLib.TellFrac()
	t.Logf("LIB: Tell before dynalloc: %d bits (frac=%d)", tellBeforeLib, tellFracBeforeLib)

	// Decode dynalloc step by step
	capsLib := celt.InitCaps(nbBands, lm, 1)
	dynallocLogpLib := 6
	totalBoostLib := 0
	tellFracDynallocLib := tellFracBeforeLib

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

		dynallocLoopLogpLib := dynallocLogpLib
		boost := 0

		budgetCheck := tellFracDynallocLib + (dynallocLoopLogpLib << bitRes)
		limit := totalBitsQ3ForDynalloc - totalBoostLib

		if budgetCheck >= limit || 0 >= capsLib[i] {
			// No budget for this band
			continue
		}

		t.Logf("LIB Band %d: width=%d quanta=%d cap=%d tellFrac=%d budgetCheck=%d limit=%d",
			i, width, quanta, capsLib[i], tellFracDynallocLib, budgetCheck, limit)

		for j := 0; tellFracDynallocLib+(dynallocLoopLogpLib<<bitRes) < totalBitsQ3ForDynalloc-totalBoostLib && boost < capsLib[i]; j++ {
			tellBeforeBit := rdLib.TellFrac()
			flag := rdLib.DecodeBit(uint(dynallocLoopLogpLib))
			tellAfterBit := rdLib.TellFrac()

			t.Logf("  LIB j=%d: logp=%d tellFrac before=%d after=%d flag=%d",
				j, dynallocLoopLogpLib, tellBeforeBit, tellAfterBit, flag)

			tellFracDynallocLib = tellAfterBit
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoostLib += quanta
			dynallocLoopLogpLib = 1
		}

		if boost > 0 && dynallocLogpLib > 2 {
			dynallocLogpLib--
		}
	}

	tellAfterLib := rdLib.Tell()
	t.Logf("LIB: Tell after dynalloc: %d bits", tellAfterLib)

	// === GOPUS ===
	t.Log("")
	t.Log("=== GOPUS dynalloc decoding ===")
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

	// Header
	rdGo.DecodeBit(15)
	rdGo.DecodeBit(1)
	transientGo := rdGo.DecodeBit(3)
	rdGo.DecodeBit(3)

	// Coarse energy
	goDecGo := celt.NewDecoder(1)
	goDecGo.DecodeCoarseEnergyWithDecoder(rdGo, nbBands, false, lm)

	// TF
	tfResGo := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, transientGo == 1, tfResGo, lm, rdGo)

	// Spread
	rdGo.DecodeICDF(spreadICDF, 5)

	tellBeforeGo := rdGo.Tell()
	tellFracBeforeGo := rdGo.TellFrac()
	t.Logf("GO: Tell before dynalloc: %d bits (frac=%d)", tellBeforeGo, tellFracBeforeGo)

	// Decode dynalloc step by step
	capsGo := celt.InitCaps(nbBands, lm, 1)
	dynallocLogpGo := 6
	totalBoostGo := 0
	tellFracDynallocGo := tellFracBeforeGo

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

		dynallocLoopLogpGo := dynallocLogpGo
		boost := 0

		budgetCheck := tellFracDynallocGo + (dynallocLoopLogpGo << bitRes)
		limit := totalBitsQ3ForDynalloc - totalBoostGo

		if budgetCheck >= limit || 0 >= capsGo[i] {
			continue
		}

		t.Logf("GO  Band %d: width=%d quanta=%d cap=%d tellFrac=%d budgetCheck=%d limit=%d",
			i, width, quanta, capsGo[i], tellFracDynallocGo, budgetCheck, limit)

		for j := 0; tellFracDynallocGo+(dynallocLoopLogpGo<<bitRes) < totalBitsQ3ForDynalloc-totalBoostGo && boost < capsGo[i]; j++ {
			tellBeforeBit := rdGo.TellFrac()
			flag := rdGo.DecodeBit(uint(dynallocLoopLogpGo))
			tellAfterBit := rdGo.TellFrac()

			t.Logf("  GO  j=%d: logp=%d tellFrac before=%d after=%d flag=%d",
				j, dynallocLoopLogpGo, tellBeforeBit, tellAfterBit, flag)

			tellFracDynallocGo = tellAfterBit
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoostGo += quanta
			dynallocLoopLogpGo = 1
		}

		if boost > 0 && dynallocLogpGo > 2 {
			dynallocLogpGo--
		}
	}

	tellAfterGo := rdGo.Tell()
	t.Logf("GO: Tell after dynalloc: %d bits", tellAfterGo)

	t.Log("")
	t.Logf("DIFF: Tell after dynalloc: lib=%d go=%d diff=%d", tellAfterLib, tellAfterGo, tellAfterLib-tellAfterGo)
}
