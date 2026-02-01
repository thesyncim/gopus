//go:build trace
// +build trace

// Package cgo traces band energy computation.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestBandEnergyComparison traces band energies and fine quant calculations.
func TestBandEnergyComparison(t *testing.T) {
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

	// Decode libopus fine quant to infer what energies libopus used
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	targetBytes := 159
	targetBits := targetBytes * 8

	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	// Skip header
	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	rdLib.DecodeBit(3)  // transient
	rdLib.DecodeBit(3)  // intra

	// Decode coarse energy
	goDecLib := celt.NewDecoder(1)
	coarseLib := goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, false, lm)

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

	// Decode trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	trimLib := rdLib.DecodeICDF(trimICDF, 7)

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

	// Now compute what band energies gopus produces
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

	// Calculate what fine quant gopus would produce
	t.Log("=== Band Energy Analysis (bands 15-20) ===")
	t.Log("")
	t.Log("Band | gopusEnergy | coarse | gopusErr | gopusQ | libQ | Match")
	t.Log("-----+-------------+--------+----------+--------+------+------")

	for i := 15; i < nbBands; i++ {
		fineBits := allocResultLib.FineBits[i]
		if fineBits == 0 {
			continue
		}

		gopusErr := energies[i] - coarseLib[i]
		scale := 1 << uint(fineBits)
		gopusQ := int(math.Floor((gopusErr + 0.5) * float64(scale)))
		if gopusQ < 0 {
			gopusQ = 0
		}
		if gopusQ >= scale {
			gopusQ = scale - 1
		}

		match := "OK"
		if gopusQ != fineQLib[i] {
			match = "DIFF"
		}

		t.Logf("  %2d |   %+.6f  | %+.4f |  %+.6f |   %d    |  %d   | %s",
			i, energies[i], coarseLib[i], gopusErr, gopusQ, fineQLib[i], match)
	}

	// For the differing bands, calculate what energy libopus must have used
	t.Log("")
	t.Log("=== Inferred libopus energies (from fine quant) ===")
	for i := 15; i < nbBands; i++ {
		fineBits := allocResultLib.FineBits[i]
		if fineBits == 0 {
			continue
		}
		q := fineQLib[i]
		scale := 1 << uint(fineBits)

		// Invert: q = floor((error + 0.5) * scale)
		// q/scale <= error + 0.5 < (q+1)/scale
		// So error is in range [q/scale - 0.5, (q+1)/scale - 0.5)
		// Midpoint: ((q+0.5)/scale) - 0.5
		midErr := (float64(q)+0.5)/float64(scale) - 0.5
		inferredEnergy := coarseLib[i] + midErr

		gopusErr := energies[i] - coarseLib[i]
		diff := energies[i] - inferredEnergy

		marker := ""
		if math.Abs(diff) > 0.01 {
			marker = " <-- differs by %.4f"
		}

		t.Logf("Band %2d: gopus_energy=%.6f, inferred_lib_energy=%.6f, diff=%.6f%s",
			i, energies[i], inferredEnergy, diff, marker)
		_ = gopusErr
	}

	// Show raw MDCT coefficients for bands 17-20
	t.Log("")
	t.Log("=== Raw energy values for problematic bands ===")

	// Get band boundaries
	for i := 17; i <= 20; i++ {
		t.Logf("Band %d: energy=%.6f (log scale), coarse=%.4f", i, energies[i], coarseLib[i])
	}
}
