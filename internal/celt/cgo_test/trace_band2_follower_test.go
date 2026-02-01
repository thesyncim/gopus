//go:build trace
// +build trace

// Package cgo traces band 2 follower computation to find the exact divergence.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceBand2FollowerDivergence traces the exact follower value for band 2.
func TestTraceBand2FollowerDivergence(t *testing.T) {
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
	bitRes := 3

	// Decode dynalloc from libopus packet
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

	tellBeforeLib := rdLib.Tell()
	t.Logf("LIB: Tell before dynalloc: %d bits", tellBeforeLib)

	// Decode dynalloc to find band 2's offset
	caps := celt.InitCaps(nbBands, lm, 1)
	dynallocLogp := 6
	totalBoost := 0
	tellFracDynalloc := rdLib.TellFrac()
	targetBytes := 159
	totalBitsQ3 := targetBytes * 8 << bitRes

	libOffsets := make([]int, nbBands)

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

		loopLogp := dynallocLogp
		boost := 0
		boostCount := 0

		budgetCheck := tellFracDynalloc + (loopLogp << bitRes)
		limit := totalBitsQ3 - totalBoost

		if budgetCheck >= limit || caps[i] <= 0 {
			continue
		}

		for j := 0; tellFracDynalloc+(loopLogp<<bitRes) < totalBitsQ3-totalBoost && boost < caps[i]; j++ {
			flag := rdLib.DecodeBit(uint(loopLogp))
			tellFracDynalloc = rdLib.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoost += quanta
			boostCount++
			loopLogp = 1
		}

		libOffsets[i] = boostCount
		if boostCount > 0 && dynallocLogp > 2 {
			dynallocLogp--
		}
	}

	t.Logf("LIB offsets (boost counts): %v", libOffsets[:10])
	t.Logf("LIB transient=%d, intra=%d", transientLib, intraLib)

	// Now decode gopus packet
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

	tellBeforeGo := rdGo.Tell()
	t.Logf("GO: Tell before dynalloc: %d bits", tellBeforeGo)

	// Decode dynalloc
	caps = celt.InitCaps(nbBands, lm, 1)
	dynallocLogp = 6
	totalBoost = 0
	tellFracDynalloc = rdGo.TellFrac()

	goOffsets := make([]int, nbBands)

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

		loopLogp := dynallocLogp
		boost := 0
		boostCount := 0

		budgetCheck := tellFracDynalloc + (loopLogp << bitRes)
		limit := totalBitsQ3 - totalBoost

		if budgetCheck >= limit || caps[i] <= 0 {
			continue
		}

		for j := 0; tellFracDynalloc+(loopLogp<<bitRes) < totalBitsQ3-totalBoost && boost < caps[i]; j++ {
			flag := rdGo.DecodeBit(uint(loopLogp))
			tellFracDynalloc = rdGo.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoost += quanta
			boostCount++
			loopLogp = 1
		}

		goOffsets[i] = boostCount
		if boostCount > 0 && dynallocLogp > 2 {
			dynallocLogp--
		}
	}

	t.Logf("GO offsets (boost counts): %v", goOffsets[:10])
	t.Logf("GO transient=%d, intra=%d", transientGo, intraGo)

	// Compare
	t.Log("")
	t.Log("=== Comparison ===")
	for i := 0; i < 10; i++ {
		diff := ""
		if libOffsets[i] != goOffsets[i] {
			diff = " *** DIFF ***"
		}
		t.Logf("Band %d: lib_offset=%d go_offset=%d%s", i, libOffsets[i], goOffsets[i], diff)
	}

	// For band 2 specifically:
	t.Log("")
	t.Log("=== Band 2 Analysis ===")
	t.Logf("Band 2: lib_offset=%d go_offset=%d", libOffsets[2], goOffsets[2])

	// What follower value would produce each offset?
	// For width=8: boost = int(follower * 8 / 6) = int(follower * 1.333)
	// offset=1: 0.75 <= follower < 1.5
	// offset=2: 1.5 <= follower < 2.25
	t.Log("")
	t.Log("For band 2 (width=8): boost = int(follower * 8 / 6)")
	t.Log("  offset=0: follower < 0.75")
	t.Log("  offset=1: 0.75 <= follower < 1.5")
	t.Log("  offset=2: 1.5 <= follower < 2.25")
	t.Log("  offset=3: 2.25 <= follower < 3.0")
}
