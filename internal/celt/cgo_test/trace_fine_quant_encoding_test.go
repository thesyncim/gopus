// Package cgo traces fine quant encoding band by band.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceFineQuantEncoding traces fine quant encoding band by band.
func TestTraceFineQuantEncoding(t *testing.T) {
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

	// === Decode libopus packet to get fine quant values ===
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	// Skip header
	rdLib.DecodeBit(15)
	rdLib.DecodeBit(1)
	rdLib.DecodeBit(3)
	rdLib.DecodeBit(3)

	// Decode coarse
	goDec := celt.NewDecoder(1)
	coarseLib := goDec.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, false, lm)

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

	fineStartBit := rdLib.Tell()

	// Decode fine quant from libopus
	libFineQ := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		fb := allocResult.FineBits[i]
		if fb == 0 {
			continue
		}
		q := rdLib.DecodeUniform(uint32(1 << uint(fb)))
		libFineQ[i] = int(q)
	}

	// === Now encode with gopus and trace fine quant encoding ===
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Get band energies from gopus
	dcRejected := goEnc.ApplyDCReject(pcm64)
	delayComp := celt.DelayCompensation
	combinedBuf := make([]float64, delayComp+len(dcRejected))
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:frameSize]
	preemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)

	overlap := celt.Overlap
	historyBuf := make([]float64, overlap)
	shortBlocks := mode.ShortBlocks
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, historyBuf, shortBlocks)

	energies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	for i := range energies {
		energies[i] = float64(float32(energies[i]))
	}

	// Calculate what fine quant gopus would produce
	gopusFineQ := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		fb := allocResult.FineBits[i]
		if fb == 0 {
			continue
		}
		err := energies[i] - coarseLib[i]
		scale := 1 << uint(fb)
		q := int(math.Floor((err + 0.5) * float64(scale)))
		if q < 0 {
			q = 0
		}
		if q >= scale {
			q = scale - 1
		}
		gopusFineQ[i] = q
	}

	// === Compare fine quant values ===
	t.Log("=== Fine Quant Comparison (bands 10-20) ===")
	t.Log("Band | libQ | gopusQ | fineBits | startBit | Match")
	t.Log("-----+------+--------+----------+----------+------")

	cumulativeBits := fineStartBit
	for i := 10; i < nbBands; i++ {
		fb := allocResult.FineBits[i]
		if fb == 0 {
			continue
		}
		lq := libFineQ[i]
		gq := gopusFineQ[i]
		match := "OK"
		if lq != gq {
			match = "DIFF"
		}
		marker := ""
		if cumulativeBits <= 128 && 128 < cumulativeBits+fb {
			marker = " <-- bit 128 here"
		}
		t.Logf("  %2d |  %2d  |   %2d   |    %d     |   %3d    | %s%s",
			i, lq, gq, fb, cumulativeBits, match, marker)
		cumulativeBits += fb
	}

	// Also show the band energies for comparison
	t.Log("")
	t.Log("=== Band Energy Details (bands 10-16) ===")
	t.Log("Band | gopusEnergy | coarse | gopusErr | calculated q | expected q")
	for i := 10; i <= 16; i++ {
		fb := allocResult.FineBits[i]
		if fb == 0 {
			continue
		}
		gerr := energies[i] - coarseLib[i]
		scale := 1 << uint(fb)
		rawQ := (gerr + 0.5) * float64(scale)
		calcQ := int(math.Floor(rawQ))
		if calcQ < 0 {
			calcQ = 0
		}
		if calcQ >= scale {
			calcQ = scale - 1
		}
		t.Logf("  %2d | %+.6f  | %+.4f | %+.6f |     %d      |    %d",
			i, energies[i], coarseLib[i], gerr, calcQ, libFineQ[i])
	}
}
