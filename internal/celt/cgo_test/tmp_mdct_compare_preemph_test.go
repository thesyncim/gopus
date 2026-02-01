//go:build trace
// +build trace

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestMDCTComparisonPreemphCoef(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	overlap := celt.Overlap
	shortBlocks := 8

	pcm := make([]float64, frameSize)
	pcmF32 := make([]float32, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm[i] = val
		pcmF32[i] = float32(val)
	}

	enc := celt.NewEncoder(1)
	enc.Reset()
	gopusPreemph := enc.ApplyPreemphasisWithScaling(pcm)

	coef := float32(celt.PreemphCoef)
	libPreemph := ApplyLibopusPreemphasis(pcmF32, coef)

	gopusMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, enc.OverlapBuffer(), shortBlocks)

	// Libopus MDCT
	libInput := make([]float32, frameSize+overlap)
	for i := 0; i < overlap; i++ {
		libInput[i] = 0
	}
	for i := 0; i < frameSize; i++ {
		libInput[overlap+i] = libPreemph[i]
	}
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}
	shift := 3
	libMDCT := make([]float32, len(gopusMDCT))
	shortSize := frameSize / shortBlocks
	for b := 0; b < shortBlocks; b++ {
		blockStart := b * shortSize
		block := make([]float32, shortSize+overlap)
		for i := 0; i < shortSize+overlap && blockStart+i < len(libInput); i++ {
			block[i] = libInput[blockStart+i]
		}
		blockMDCT := mode.MDCTForward(block, shift)
		for i, v := range blockMDCT {
			idx := b + i*shortBlocks
			if idx < len(libMDCT) {
				libMDCT[idx] = v
			}
		}
	}

	// Compare a few coefficients
	maxDiff := 0.0
	maxIdx := 0
	for i := 0; i < len(gopusMDCT) && i < len(libMDCT); i++ {
		diff := math.Abs(gopusMDCT[i] - float64(libMDCT[i]))
		if diff > maxDiff {
			maxDiff = diff
			maxIdx = i
		}
	}
	if maxDiff > 0 {
		t.Logf("Max MDCT diff = %.6f at idx %d", maxDiff, maxIdx)
	}
	if maxDiff > 1e-3 {
		t.Logf("gopus[%d]=%.6f lib[%d]=%.6f", maxIdx, gopusMDCT[maxIdx], maxIdx, libMDCT[maxIdx])
	}
}
