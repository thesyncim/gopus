// Package cgo traces fine energy encoding in detail.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const bitResFE = 3

// TestFineEnergyDetailedTrace traces fine energy encoding step by step.
func TestFineEnergyDetailedTrace(t *testing.T) {
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
	t.Logf("libopus packet: %d bytes", len(libPayload))

	// Setup gopus encoder
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Compute energies
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

	// Initialize encoder up to fine energy
	targetBits := 159 * 8
	targetBytes := 159
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	re.Shrink(uint32(targetBytes))
	goEnc.SetRangeEncoder(re)

	// Header flags
	re.EncodeBit(0, 15)
	re.EncodeBit(0, 1)
	re.EncodeBit(1, 3)
	re.EncodeBit(1, 3)

	// Coarse energy - get quantized values
	quantizedEnergies := goEnc.EncodeCoarseEnergy(energies, nbBands, true, lm)

	// Compute allocation (skipping TF, spread, dynalloc, trim for now - just get fineBits)
	normL := goEnc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)
	effectiveBytes := targetBytes
	tfEstimate := 0.2
	prev1LogE := make([]float64, nbBands)

	dynallocResult := celt.DynallocAnalysis(
		energies, energies, prev1LogE,
		nbBands, 0, nbBands, 1, 16, lm,
		nil, effectiveBytes,
		true, false, false, 0.0, 0.0,
	)

	tfRes, tfSelect := celt.TFAnalysis(normL, len(normL), nbBands, true, lm, tfEstimate, effectiveBytes, dynallocResult.Importance)
	celt.TFEncodeWithSelect(re, 0, nbBands, true, tfRes, lm, tfSelect)

	spreadICDF := []uint8{25, 23, 2, 0}
	re.EncodeICDF(2, spreadICDF, 5)

	// Dynalloc
	caps := celt.InitCaps(nbBands, lm, 1)
	offsets := make([]int, nbBands)
	if dynallocResult.Offsets != nil {
		copy(offsets, dynallocResult.Offsets)
	}

	totalBitsQ3ForDynalloc := targetBits << bitResFE
	dynallocLogp := 6
	totalBoost := 0
	tellFracDynalloc := re.TellFrac()

	for i := 0; i < nbBands; i++ {
		width := celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := 6 << bitResFE
		if width > innerMax {
			innerMax = width
		}
		quanta := width << bitResFE
		if quanta > innerMax {
			quanta = innerMax
		}

		dynallocLoopLogp := dynallocLogp
		boost := 0

		for j := 0; tellFracDynalloc+(dynallocLoopLogp<<bitResFE) < totalBitsQ3ForDynalloc-totalBoost && boost < caps[i]; j++ {
			flag := 0
			if j < offsets[i] {
				flag = 1
			}
			re.EncodeBit(flag, uint(dynallocLoopLogp))
			tellFracDynalloc = re.TellFrac()
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

	// Trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	equivRate := celt.ComputeEquivRate(effectiveBytes, 1, lm, bitrate)
	allocTrim := celt.AllocTrimAnalysis(normL, energies, nbBands, lm, 1, nil, nbBands, tfEstimate, equivRate, 0, 0)
	re.EncodeICDF(allocTrim, trimICDF, 7)

	// Allocation
	bitsUsed := re.TellFrac()
	totalBitsQ3 := (targetBits << bitResFE) - bitsUsed - 1
	antiCollapseRsv := 1 << bitResFE
	totalBitsQ3 -= antiCollapseRsv

	allocResult := celt.ComputeAllocationWithEncoder(
		re, totalBitsQ3>>bitResFE,
		nbBands, 1, caps, offsets, allocTrim,
		nbBands, false, lm, 0, nbBands-1,
	)

	t.Log("=== Fine Energy Encoding Detail ===")
	t.Logf("tell before fine energy: %d bits", re.Tell())

	// Now trace fine energy encoding band by band
	t.Log("\nFine bits allocation and residuals:")
	for i := 0; i < nbBands; i++ {
		fineBits := allocResult.FineBits[i]
		if fineBits == 0 {
			continue
		}

		error := energies[i] - quantizedEnergies[i]
		scale := 1 << uint(fineBits)
		q := int(math.Floor((error + 0.5) * float64(scale)))
		if q < 0 {
			q = 0
		}
		if q >= scale {
			q = scale - 1
		}

		tellBefore := re.Tell()
		t.Logf("  band %2d: fineBits=%d error=%.6f q=%d tell=%d",
			i, fineBits, error, q, tellBefore)
	}

	// Actually encode fine energy
	goEnc.EncodeFineEnergy(energies, quantizedEnergies, nbBands, allocResult.FineBits)
	t.Logf("\ntell after fine energy: %d bits", re.Tell())

	// Show bytes around divergence
	t.Log("\nFine energy spans bits 79-144")
	t.Log("Byte 16 = bits 128-135")
	t.Log("So the divergence at bit 128 is in fine energy encoding")

	// The divergence is at bit 128, which is bit 49 into fine energy encoding
	// (128 - 79 = 49)
	// Each fine quant uses fineBits bits per band
	// Need to find which band is being encoded around bit 49 of fine energy

	t.Log("\nCalculating which band is encoded at bit 128:")
	cumulativeBits := 0
	for i := 0; i < nbBands; i++ {
		if allocResult.FineBits[i] == 0 {
			continue
		}
		cumulativeBits += allocResult.FineBits[i]
		if cumulativeBits > 49 { // bit 49 into fine energy (128 - 79)
			t.Logf("  Bit 128 (~bit 49 into fine energy) is in band %d", i)
			t.Logf("  fineBits[%d] = %d", i, allocResult.FineBits[i])
			t.Logf("  cumulative bits at this point: %d", cumulativeBits)
			break
		}
	}
}
