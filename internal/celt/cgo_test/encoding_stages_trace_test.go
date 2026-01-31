// Package cgo traces encoding stages to find the exact divergence point.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const bitRes = 3 // matches celt.bitRes

// TestEncodingStagesVsLibopus traces each encoding stage.
func TestEncodingStagesVsLibopus(t *testing.T) {
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

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}
	libPayload := libPacket[1:]
	t.Logf("libopus: %d bytes, final_range=0x%08X", len(libPayload), libEnc.GetFinalRange())

	// Now manually trace gopus encoding
	t.Log("\n=== GOPUS Manual Trace ===")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Setup encoder
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Get DC-rejected and pre-emphasized samples
	dcRejected := goEnc.ApplyDCReject(pcm64)
	delayComp := celt.DelayCompensation
	combinedBuf := make([]float64, delayComp+len(dcRejected))
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:frameSize]
	preemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)

	// Force transient for first frame
	transient := true
	shortBlocks := mode.ShortBlocks

	// MDCT
	overlap := celt.Overlap
	historyBuf := make([]float64, overlap)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, historyBuf, shortBlocks)

	// Band energies
	energies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	for i := range energies {
		energies[i] = float64(float32(energies[i]))
	}

	// Initialize range encoder
	targetBits := 159 * 8
	targetBytes := 159
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	re.Shrink(uint32(targetBytes))
	goEnc.SetRangeEncoder(re)

	// Stage 1: Header flags
	re.EncodeBit(0, 15) // silence
	re.EncodeBit(0, 1)  // postfilter
	re.EncodeBit(1, 3)  // transient
	re.EncodeBit(1, 3)  // intra
	tell1 := re.Tell()
	t.Logf("After header flags: tell=%d bits", tell1)

	// Stage 2: Coarse energy
	quantizedEnergies := goEnc.EncodeCoarseEnergy(energies, nbBands, true, lm)
	tell2 := re.Tell()
	t.Logf("After coarse energy: tell=%d bits (+%d)", tell2, tell2-tell1)

	// Get normalized coefficients
	normL := goEnc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)

	// Stage 3: TF encoding
	effectiveBytes := targetBytes
	tfEstimate := 0.2

	prev1LogE := make([]float64, nbBands)
	bandLogE2 := energies

	dynallocResult := celt.DynallocAnalysis(
		energies, bandLogE2, prev1LogE,
		nbBands, 0, nbBands, 1, 16, lm,
		nil, effectiveBytes,
		transient, false, false,
		0.0, 0.0,
	)

	tfRes, tfSelect := celt.TFAnalysis(normL, len(normL), nbBands, transient, lm, tfEstimate, effectiveBytes, dynallocResult.Importance)
	celt.TFEncodeWithSelect(re, 0, nbBands, transient, tfRes, lm, tfSelect)
	tell3 := re.Tell()
	t.Logf("After TF encoding: tell=%d bits (+%d)", tell3, tell3-tell2)

	// Stage 4: Spread
	spreadICDF := []uint8{25, 23, 2, 0}
	spreadNormal := 2
	re.EncodeICDF(spreadNormal, spreadICDF, 5)
	tell4 := re.Tell()
	t.Logf("After spread: tell=%d bits (+%d)", tell4, tell4-tell3)

	// Stage 5: Dynalloc
	caps := celt.InitCaps(nbBands, lm, 1)
	offsets := make([]int, nbBands)
	if dynallocResult.Offsets != nil {
		copy(offsets, dynallocResult.Offsets)
	}

	totalBitsQ3ForDynalloc := targetBits << bitRes
	dynallocLogp := 6
	totalBoost := 0
	tellFracDynalloc := re.TellFrac()

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
	tell5 := re.Tell()
	t.Logf("After dynalloc: tell=%d bits (+%d)", tell5, tell5-tell4)

	// Stage 6: Trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	equivRate := celt.ComputeEquivRate(effectiveBytes, 1, lm, bitrate)
	allocTrim := celt.AllocTrimAnalysis(normL, energies, nbBands, lm, 1, nil, nbBands, tfEstimate, equivRate, 0, 0)
	re.EncodeICDF(allocTrim, trimICDF, 7)
	tell6 := re.Tell()
	t.Logf("After trim: tell=%d bits (+%d)", tell6, tell6-tell5)

	// Stage 7: Allocation
	bitsUsed := re.TellFrac()
	totalBitsQ3 := (targetBits << bitRes) - bitsUsed - 1
	antiCollapseRsv := 1 << bitRes
	totalBitsQ3 -= antiCollapseRsv

	allocResult := celt.ComputeAllocationWithEncoder(
		re,
		totalBitsQ3>>bitRes,
		nbBands, 1, caps, offsets, allocTrim,
		nbBands, false, lm, 0, nbBands-1,
	)
	tell7 := re.Tell()
	t.Logf("After allocation (skip/intensity): tell=%d bits (+%d), codedBands=%d", tell7, tell7-tell6, allocResult.CodedBands)

	// Stage 8: Fine energy
	goEnc.EncodeFineEnergy(energies, quantizedEnergies, nbBands, allocResult.FineBits)
	tell8 := re.Tell()
	t.Logf("After fine energy: tell=%d bits (+%d)", tell8, tell8-tell7)

	// Key check: where are we relative to byte 16?
	byte16Bit := 16 * 8
	t.Logf("\n*** Bit 128 (byte 16) position analysis ***")
	t.Logf("tell=%d, byte 16 starts at bit %d", tell8, byte16Bit)
	if tell8 < byte16Bit {
		t.Logf("Fine energy ends BEFORE byte 16 - divergence is in PVQ bands")
		t.Logf("PVQ encoding starts at bit %d", tell8)
	} else {
		t.Logf("Fine energy extends PAST byte 16 - divergence is in fine energy")
	}

}
