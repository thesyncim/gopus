//go:build trace
// +build trace

// Package cgo traces PVQ indices across all bands to find first divergence.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// decodePVQIndices decodes PVQ indices from a CELT payload using the same
// decode pipeline as trace_real_encoding_test.
func decodePVQIndices(payload []byte, nbBands, lm, channels int) ([]uint32, []int, []uint32, int) {
	rd := &rangecoding.Decoder{}
	rd.Init(payload)

	// Header
	rd.DecodeBit(15) // silence
	rd.DecodeBit(1)  // postfilter
	rd.DecodeBit(3)  // transient
	intra := rd.DecodeBit(3)

	// Coarse energy
	dec := celt.NewDecoder(channels)
	_ = dec.DecodeCoarseEnergyWithDecoder(rd, nbBands, intra == 1, lm)

	// TF
	for i := 0; i < nbBands; i++ {
		rd.DecodeBit(1)
	}
	rd.DecodeBit(1) // tf_select

	// Spread
	spreadICDF := []uint8{25, 23, 2, 0}
	rd.DecodeICDF(spreadICDF, 5)

	// Dynalloc
	bitRes := 3
	caps := celt.InitCaps(nbBands, lm, channels)
	offsets := make([]int, nbBands)
	targetBits := len(payload) * 8
	totalBitsQ3ForDynalloc := targetBits << bitRes
	dynallocLogp := 6
	totalBoost := 0
	tellFracDynalloc := rd.TellFrac()

	for i := 0; i < nbBands; i++ {
		width := channels * celt.ScaledBandWidth(i, 120<<lm)
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

	// Trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	trim := rd.DecodeICDF(trimICDF, 7)

	// Allocation
	bitsUsed := rd.TellFrac()
	antiCollapseRsv := 1 << bitRes
	totalBitsQ3 := (targetBits << bitRes) - bitsUsed - 1 - antiCollapseRsv

	allocResult := celt.ComputeAllocationWithDecoder(
		rd, totalBitsQ3>>bitRes,
		nbBands, channels, caps, offsets, trim,
		nbBands, false, lm,
	)

	// Fine energy (raw bits)
	for i := 0; i < nbBands; i++ {
		fineBits := allocResult.FineBits[i]
		if fineBits > 0 {
			rd.DecodeRawBits(uint(fineBits))
		}
	}

	indices := make([]uint32, nbBands)
	kVals := make([]int, nbBands)
	vSizes := make([]uint32, nbBands)

	M := 1 << lm
	for band := 0; band < nbBands; band++ {
		bandStart := celt.EBands[band] * M
		bandEnd := celt.EBands[band+1] * M
		n := bandEnd - bandStart

		bits := allocResult.BandBits[band]
		q := celt.BitsToPulsesExport(band, lm, bits)
		k := celt.GetPulsesExport(q)
		kVals[band] = k
		if k <= 0 || n <= 0 {
			continue
		}
		vsize := celt.PVQ_V(n, k)
		vSizes[band] = vsize
		if vsize > 0 {
			indices[band] = rd.DecodeUniform(vsize)
		}
	}

	return indices, kVals, vSizes, allocResult.CodedBands
}

// TestTracePVQIndicesAllBands finds the first band where PVQ indices differ.
func TestTracePVQIndicesAllBands(t *testing.T) {
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

	libIdx, libK, libV, libCoded := decodePVQIndices(libPayload, nbBands, lm, 1)
	goIdx, goK, goV, goCoded := decodePVQIndices(goPacket, nbBands, lm, 1)

	t.Logf("codedBands: lib=%d go=%d", libCoded, goCoded)

	firstDiff := -1
	for band := 0; band < nbBands; band++ {
		if libK[band] != goK[band] || libV[band] != goV[band] || libIdx[band] != goIdx[band] {
			firstDiff = band
			break
		}
	}
	if firstDiff < 0 {
		t.Log("PVQ indices match for all bands")
		return
	}

	t.Logf("First PVQ diff at band %d", firstDiff)
	for band := firstDiff; band < nbBands && band < firstDiff+4; band++ {
		t.Logf("Band %2d: lib(k=%d, v=%d, idx=%d) go(k=%d, v=%d, idx=%d)",
			band, libK[band], libV[band], libIdx[band], goK[band], goV[band], goIdx[band])
	}
	t.Fatalf("PVQ indices diverge at band %d", firstDiff)
}
