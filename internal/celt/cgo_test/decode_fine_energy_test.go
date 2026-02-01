// Package cgo decodes libopus fine energy to compare indices.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const bitResDE = 3

// TestDecodeFineEnergyComparison traces fine energy indices from both encoders.
func TestDecodeFineEnergyComparison(t *testing.T) {
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

	// Compute energies through gopus pipeline
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

	// Initialize range encoder for gopus
	targetBytes := 159
	targetBits := targetBytes * 8
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	re.Shrink(uint32(targetBytes))
	goEnc.SetRangeEncoder(re)

	// Header flags
	re.EncodeBit(0, 15) // silence
	re.EncodeBit(0, 1)  // postfilter
	re.EncodeBit(1, 3)  // transient
	re.EncodeBit(1, 3)  // intra

	// Coarse energy
	quantizedEnergies := goEnc.EncodeCoarseEnergy(energies, nbBands, true, lm)

	// Get normalized coefficients for allocation
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

	// Spread
	spreadICDF := []uint8{25, 23, 2, 0}
	re.EncodeICDF(2, spreadICDF, 5)

	// Dynalloc
	caps := celt.InitCaps(nbBands, lm, 1)
	offsets := make([]int, nbBands)
	if dynallocResult.Offsets != nil {
		copy(offsets, dynallocResult.Offsets)
	}

	totalBitsQ3ForDynalloc := targetBits << bitResDE
	dynallocLogp := 6
	totalBoost := 0
	tellFracDynalloc := re.TellFrac()

	for i := 0; i < nbBands; i++ {
		width := celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := 6 << bitResDE
		if width > innerMax {
			innerMax = width
		}
		quanta := width << bitResDE
		if quanta > innerMax {
			quanta = innerMax
		}

		dynallocLoopLogpD := dynallocLogp
		boost := 0

		for j := 0; tellFracDynalloc+(dynallocLoopLogpD<<bitResDE) < totalBitsQ3ForDynalloc-totalBoost && boost < caps[i]; j++ {
			flag := 0
			if j < offsets[i] {
				flag = 1
			}
			re.EncodeBit(flag, uint(dynallocLoopLogpD))
			tellFracDynalloc = re.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoost += quanta
			dynallocLoopLogpD = 1
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
	totalBitsQ3 := (targetBits << bitResDE) - bitsUsed - 1
	antiCollapseRsv := 1 << bitResDE
	totalBitsQ3 -= antiCollapseRsv

	allocResult := celt.ComputeAllocationWithEncoder(
		re, totalBitsQ3,
		nbBands, 1, caps, offsets, allocTrim,
		nbBands, false, lm, 0, nbBands-1,
	)

	t.Log("=== GOPUS Fine Energy Analysis ===")
	t.Logf("Tell before fine energy: %d bits", re.Tell())

	// Calculate gopus fine quant indices
	gopusFineQ := make([]int, nbBands)
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
		gopusFineQ[i] = q
	}

	// Now decode the libopus packet to get fine energy indices
	// We'll use gopus decoder with known allocation result
	t.Log("")
	t.Log("=== LIBOPUS Decode (using gopus decoder) ===")

	// Create a decoder
	goDec := celt.NewDecoder(1)

	rd := &rangecoding.Decoder{}
	rd.Init(libPayload)

	// Decode header
	silenceDec := rd.DecodeBit(15)
	postfilterDec := rd.DecodeBit(1)
	transientDec := rd.DecodeBit(3)
	intraDec := rd.DecodeBit(3)
	t.Logf("Header: silence=%d postfilter=%d transient=%d intra=%d",
		silenceDec, postfilterDec, transientDec, intraDec)

	// Decode coarse energy
	decQuantized := goDec.DecodeCoarseEnergyWithDecoder(rd, nbBands, intraDec == 1, lm)
	t.Logf("Tell after coarse: %d bits", rd.Tell())

	// Decode TF
	for i := 0; i < nbBands; i++ {
		rd.DecodeBit(1)
	}
	rd.DecodeBit(1) // tfSelect
	t.Logf("Tell after TF: %d bits", rd.Tell())

	// Decode spread
	spreadDecICDF := []uint8{25, 23, 2, 0}
	spreadDec := rd.DecodeICDF(spreadDecICDF, 5)
	t.Logf("Spread: %d, Tell after spread: %d bits", spreadDec, rd.Tell())

	// Decode dynalloc
	capsD := celt.InitCaps(nbBands, lm, 1)
	offsetsDec := make([]int, nbBands)
	totalBitsQ3ForDynallocD := targetBits << bitResDE
	dynallocLogpD := 6
	totalBoostD := 0
	tellFracDynallocD := rd.TellFrac()

	for i := 0; i < nbBands; i++ {
		width := celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := 6 << bitResDE
		if width > innerMax {
			innerMax = width
		}
		quanta := width << bitResDE
		if quanta > innerMax {
			quanta = innerMax
		}

		dynallocLoopLogpD := dynallocLogpD
		boost := 0

		for j := 0; tellFracDynallocD+(dynallocLoopLogpD<<bitResDE) < totalBitsQ3ForDynallocD-totalBoostD && boost < capsD[i]; j++ {
			flag := rd.DecodeBit(uint(dynallocLoopLogpD))
			tellFracDynallocD = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoostD += quanta
			dynallocLoopLogpD = 1
		}

		if boost > 0 && dynallocLogpD > 2 {
			dynallocLogpD--
		}
		offsetsDec[i] = boost
	}
	t.Logf("Tell after dynalloc: %d bits", rd.Tell())

	// Decode trim
	trimDecICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	trimDec := rd.DecodeICDF(trimDecICDF, 7)
	t.Logf("Trim: %d, Tell after trim: %d bits", trimDec, rd.Tell())

	// Compute allocation using decoder
	bitsUsedD := rd.TellFrac()
	totalBitsQ3D := (targetBits << bitResDE) - bitsUsedD - 1
	antiCollapseRsvD := 1 << bitResDE
	totalBitsQ3D -= antiCollapseRsvD

	allocResultD := celt.ComputeAllocationWithDecoder(
		rd, totalBitsQ3D>>bitResDE,
		nbBands, 1, capsD, offsetsDec, trimDec,
		nbBands, false, lm,
	)
	t.Logf("Tell after allocation: %d bits", rd.Tell())

	// Decode fine energy indices
	t.Logf("Tell before fine energy decode: %d bits", rd.Tell())

	libopusFineQ := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		fineBits := allocResultD.FineBits[i]
		if fineBits == 0 {
			continue
		}
		q := rd.DecodeUniform(uint32(1 << uint(fineBits)))
		libopusFineQ[i] = int(q)
	}

	t.Logf("Tell after fine energy decode: %d bits", rd.Tell())

	// Compare fine quant indices
	t.Log("")
	t.Log("=== Fine Energy Index Comparison ===")
	t.Log("Band | gopusQ | libopusQ | fineBits | gopusErr | Match")
	t.Log("-----+--------+----------+----------+----------+------")
	divergeFound := false
	for i := 0; i < nbBands; i++ {
		fb := allocResult.FineBits[i]
		if fb == 0 {
			continue
		}
		gq := gopusFineQ[i]
		lq := libopusFineQ[i]
		gerr := energies[i] - quantizedEnergies[i]
		match := "OK"
		if gq != lq {
			match = "DIFF!"
			divergeFound = true
		}
		t.Logf("  %2d |   %3d  |    %3d   |    %d     | %+.4f | %s", i, gq, lq, fb, gerr, match)
	}

	if !divergeFound {
		t.Log("")
		t.Log("All fine quant indices match! Issue must be elsewhere.")
	} else {
		t.Log("")
		t.Log("Fine quant indices differ! This explains the byte divergence.")
	}

	// Show coarse energy comparison
	t.Log("")
	t.Log("=== Coarse Energy Comparison ===")
	t.Log("Band | gopusCoarse | libopusCoarse | Diff")
	for i := 0; i < nbBands; i++ {
		diff := quantizedEnergies[i] - decQuantized[i]
		marker := ""
		if math.Abs(diff) > 0.001 {
			marker = " <-- DIFF"
		}
		t.Logf("  %2d |   %+.4f    |    %+.4f    | %+.6f%s",
			i, quantizedEnergies[i], decQuantized[i], diff, marker)
	}
}
