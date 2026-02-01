//go:build trace
// +build trace

// Package cgo traces exact PVQ input vs libopus encoded direction
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTracePVQExactInput traces the exact input to gopus PVQ encoder.
func TestTracePVQExactInput(t *testing.T) {
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

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	shortBlocks := mode.ShortBlocks
	M := 1 << lm

	// === GOPUS PIPELINE ===
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// 1. Pre-emphasis
	gopusPreemph := goEnc.ApplyPreemphasisWithScaling(pcm64)

	// 2. MDCT with transient (short blocks)
	gopusMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, goEnc.OverlapBuffer(), shortBlocks)

	// 3. Band energies
	gopusEnergies := goEnc.ComputeBandEnergies(gopusMDCT, nbBands, frameSize)

	// 4. Normalize bands - THIS is what gets passed to PVQ
	gopusNorm := goEnc.NormalizeBandsToArray(gopusMDCT, gopusEnergies, nbBands, frameSize)

	t.Log("=== GOPUS Normalized Coefficients (Band 0) ===")
	bandStart := celt.EBands[0] * M
	bandEnd := celt.EBands[1] * M
	n := bandEnd - bandStart
	t.Logf("Band 0: indices %d to %d (n=%d)", bandStart, bandEnd, n)
	for i := bandStart; i < bandEnd; i++ {
		t.Logf("  gopusNorm[%d] = %+.10f", i, gopusNorm[i])
	}

	// Compute L2 norm
	gopusL2 := 0.0
	for i := bandStart; i < bandEnd; i++ {
		gopusL2 += gopusNorm[i] * gopusNorm[i]
	}
	gopusL2 = math.Sqrt(gopusL2)
	t.Logf("L2 norm: %.10f", gopusL2)

	// === LIBOPUS PACKET DECODING ===
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

	// Decode to get PVQ indices
	rd := &rangecoding.Decoder{}
	rd.Init(libPayload)

	// Skip header
	rd.DecodeBit(15) // silence
	rd.DecodeBit(1)  // postfilter
	rd.DecodeBit(3)  // transient
	intra := rd.DecodeBit(3)

	// Decode coarse energy
	goDec := celt.NewDecoder(1)
	goDec.DecodeCoarseEnergyWithDecoder(rd, nbBands, intra == 1, lm)

	// Decode TF
	for i := 0; i < nbBands; i++ {
		rd.DecodeBit(1)
	}
	rd.DecodeBit(1) // tf_select

	// Decode spread
	spreadICDF := []uint8{25, 23, 2, 0}
	rd.DecodeICDF(spreadICDF, 5)

	// Decode dynalloc
	bitRes := 3
	caps := celt.InitCaps(nbBands, lm, 1)
	offsets := make([]int, nbBands)
	targetBytes := 159
	targetBits := targetBytes * 8
	totalBitsQ3ForDynalloc := targetBits << bitRes
	dynallocLogp := 6
	totalBoost := 0
	tellFracDynalloc := rd.TellFrac()

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
			flag := rd.DecodeBit(uint(dynallocLoopLogp))
			tellFracDynalloc = rd.TellFrac()
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
	trim := rd.DecodeICDF(trimICDF, 7)

	// Compute allocation
	bitsUsed := rd.TellFrac()
	totalBitsQ3 := (targetBits << bitRes) - bitsUsed - 1
	antiCollapseRsv := 1 << bitRes
	totalBitsQ3 -= antiCollapseRsv

	allocResult := celt.ComputeAllocationWithDecoder(
		rd, totalBitsQ3>>bitRes,
		nbBands, 1, caps, offsets, trim,
		nbBands, false, lm,
	)

	// Decode fine energy
	for i := 0; i < nbBands; i++ {
		fineBits := allocResult.FineBits[i]
		if fineBits > 0 {
			rd.DecodeRawBits(uint(fineBits))
		}
	}

	// Now decode PVQ for band 0
	t.Log("")
	t.Log("=== LIBOPUS PVQ Decoding (Band 0) ===")
	pulseBits := allocResult.BandBits[0]
	q := celt.BitsToPulsesExport(0, lm, pulseBits)
	k := celt.GetPulsesExport(q)
	t.Logf("Band 0: pulseBits=%d, q=%d, k=%d", pulseBits, q, k)

	if k > 0 {
		vSize := celt.PVQ_V(n, k)
		idx := rd.DecodeUniform(vSize)
		pulses := celt.DecodePulses(idx, n, k)

		// Convert pulses to direction vector
		pulseNorm := 0.0
		for _, p := range pulses {
			pulseNorm += float64(p * p)
		}
		pulseNorm = math.Sqrt(pulseNorm)

		t.Logf("vSize=%d, idx=%d, pulseNorm=%.6f", vSize, idx, pulseNorm)

		t.Log("")
		t.Log("=== LIBOPUS Decoded Direction (Band 0) ===")
		for i := 0; i < n; i++ {
			dir := float64(pulses[i]) / pulseNorm
			t.Logf("  libDir[%d] = %+.10f (pulse=%d)", i, dir, pulses[i])
		}

		// Compute dot product
		t.Log("")
		t.Log("=== Comparison ===")
		dotProduct := 0.0
		for i := 0; i < n; i++ {
			libDir := float64(pulses[i]) / pulseNorm
			dotProduct += libDir * gopusNorm[bandStart+i]
		}
		t.Logf("Dot product (should be ~1.0): %.10f", dotProduct)

		// Check if there's a sign flip pattern
		t.Log("")
		t.Log("=== Sign analysis ===")
		sameSign := 0
		diffSign := 0
		for i := 0; i < n; i++ {
			libDir := float64(pulses[i]) / pulseNorm
			gopusVal := gopusNorm[bandStart+i]
			if (libDir >= 0) == (gopusVal >= 0) {
				sameSign++
			} else {
				diffSign++
			}
		}
		t.Logf("Same sign: %d, Different sign: %d", sameSign, diffSign)

		// What PVQ search would produce from gopus normalized
		t.Log("")
		t.Log("=== Testing PVQ search on gopus normalized ===")
		xBand := make([]float64, n)
		copy(xBand, gopusNorm[bandStart:bandEnd])

		searchPulses, _ := celt.ExportedOpPVQSearch(xBand, k)
		searchNorm := 0.0
		for _, p := range searchPulses {
			searchNorm += float64(p * p)
		}
		searchNorm = math.Sqrt(searchNorm)

		t.Log("Search result:")
		for i := 0; i < n; i++ {
			searchDir := float64(searchPulses[i]) / searchNorm
			libDir := float64(pulses[i]) / pulseNorm
			marker := ""
			if searchPulses[i] != pulses[i] {
				marker = " <-- DIFFERS"
			}
			t.Logf("  gopusSearch[%d] = %+.6f (pulse=%d) vs lib=%+.6f (pulse=%d)%s",
				i, searchDir, searchPulses[i], libDir, pulses[i], marker)
		}

		// Encode search result
		searchIdx := celt.EncodePulses(searchPulses, n, k)
		t.Logf("")
		t.Logf("gopus search index: %d vs libopus index: %d", searchIdx, idx)
		if searchIdx == idx {
			t.Log("INDICES MATCH!")
		} else {
			t.Log("INDICES DIFFER!")
		}
	}
}
