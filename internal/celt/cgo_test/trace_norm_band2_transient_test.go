//go:build trace
// +build trace

// Package cgo compares normalized coefficients for band 2 in transient mode.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestNormalizedCoeffsBand2Transient compares gopus vs libopus normalized coeffs for band 2.
func TestNormalizedCoeffsBand2Transient(t *testing.T) {
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

	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Apply DC reject + delay compensation (match encoder path).
	dcRejected := goEnc.ApplyDCReject(pcm64)
	delayComp := celt.DelayCompensation
	combined := make([]float64, delayComp+len(dcRejected))
	copy(combined[delayComp:], dcRejected)
	samplesForFrame := combined[:frameSize]

	// Gopus preemph + MDCT (short blocks)
	goPreemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)
	goMDCT := celt.ComputeMDCTWithHistory(goPreemph, make([]float64, celt.Overlap), shortBlocks)
	goEnergies := goEnc.ComputeBandEnergies(goMDCT, nbBands, frameSize)
	goNorm := goEnc.NormalizeBandsToArray(goMDCT, goEnergies, nbBands, frameSize)

	// Libopus preemph + MDCT (short blocks)
	samplesForFrame32 := make([]float32, frameSize)
	for i := 0; i < frameSize; i++ {
		samplesForFrame32[i] = float32(samplesForFrame[i])
	}
	libPreemph := ApplyLibopusPreemphasis(samplesForFrame32, float32(celt.PreemphCoef))
	modeLib := GetCELTMode48000_960()
	if modeLib == nil {
		t.Fatal("failed to get libopus CELT mode")
	}
	// Build input buffer: history (zeros) + preemph
	libInput := make([]float32, frameSize+celt.Overlap)
	copy(libInput[celt.Overlap:], libPreemph)
	// Short-block MDCT (shift=3) and interleave
	shortSize := frameSize / shortBlocks
	libMDCT := make([]float32, frameSize)
	for b := 0; b < shortBlocks; b++ {
		blockStart := b * shortSize
		blockInput := make([]float32, shortSize+celt.Overlap)
		copy(blockInput, libInput[blockStart:blockStart+shortSize+celt.Overlap])
		blockMDCT := modeLib.MDCTForward(blockInput, 3)
		for i, v := range blockMDCT {
			outIdx := b + i*shortBlocks
			if outIdx < len(libMDCT) {
				libMDCT[outIdx] = v
			}
		}
	}
	libBandE := ComputeLibopusBandEnergyLinear(libMDCT, nbBands, frameSize, lm)
	libNorm := NormaliseLibopusBands(libMDCT, libBandE, frameSize, nbBands, lm)

	band := 2
	bandStart := celt.EBands[band] * M
	bandEnd := celt.EBands[band+1] * M
	if bandEnd > frameSize {
		bandEnd = frameSize
	}
	if bandStart < 0 || bandStart >= bandEnd {
		t.Fatalf("invalid band range for band %d", band)
	}

	maxDiff := 0.0
	maxIdx := -1
	for i := bandStart; i < bandEnd; i++ {
		diff := math.Abs(float64(libNorm[i]) - goNorm[i])
		if diff > maxDiff {
			maxDiff = diff
			maxIdx = i
		}
	}

	t.Logf("Band %d range [%d-%d) maxDiff=%.10f at idx %d", band, bandStart, bandEnd, maxDiff, maxIdx)
	t.Log("Index | libNorm | goNorm | diff")
	for i := bandStart; i < bandEnd && i < bandStart+4; i++ {
		diff := float64(libNorm[i]) - goNorm[i]
		t.Logf("%5d | %+0.10f | %+0.10f | %+0.10f", i, libNorm[i], goNorm[i], diff)
	}

	if maxDiff > 1e-6 {
		t.Fatalf("normalized coeffs differ in band %d (maxDiff=%.10f)", band, maxDiff)
	}
}

// TestNormalizedCoeffsBand6Transient compares gopus vs libopus normalized coeffs for band 6.
func TestNormalizedCoeffsBand6Transient(t *testing.T) {
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

	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Apply DC reject + delay compensation (match encoder path).
	dcRejected := goEnc.ApplyDCReject(pcm64)
	delayComp := celt.DelayCompensation
	combined := make([]float64, delayComp+len(dcRejected))
	copy(combined[delayComp:], dcRejected)
	samplesForFrame := combined[:frameSize]

	// Gopus preemph + MDCT (short blocks)
	goPreemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)
	goMDCT := celt.ComputeMDCTWithHistory(goPreemph, make([]float64, celt.Overlap), shortBlocks)
	goEnergies := goEnc.ComputeBandEnergies(goMDCT, nbBands, frameSize)
	goNorm := goEnc.NormalizeBandsToArray(goMDCT, goEnergies, nbBands, frameSize)

	// Libopus preemph + MDCT (short blocks)
	samplesForFrame32 := make([]float32, frameSize)
	for i := 0; i < frameSize; i++ {
		samplesForFrame32[i] = float32(samplesForFrame[i])
	}
	libPreemph := ApplyLibopusPreemphasis(samplesForFrame32, float32(celt.PreemphCoef))
	modeLib := GetCELTMode48000_960()
	if modeLib == nil {
		t.Fatal("failed to get libopus CELT mode")
	}
	// Build input buffer: history (zeros) + preemph
	libInput := make([]float32, frameSize+celt.Overlap)
	copy(libInput[celt.Overlap:], libPreemph)
	// Short-block MDCT (shift=3) and interleave
	shortSize := frameSize / shortBlocks
	libMDCT := make([]float32, frameSize)
	for b := 0; b < shortBlocks; b++ {
		blockStart := b * shortSize
		blockInput := make([]float32, shortSize+celt.Overlap)
		copy(blockInput, libInput[blockStart:blockStart+shortSize+celt.Overlap])
		blockMDCT := modeLib.MDCTForward(blockInput, 3)
		for i, v := range blockMDCT {
			outIdx := b + i*shortBlocks
			if outIdx < len(libMDCT) {
				libMDCT[outIdx] = v
			}
		}
	}
	libBandE := ComputeLibopusBandEnergyLinear(libMDCT, nbBands, frameSize, lm)
	libNorm := NormaliseLibopusBands(libMDCT, libBandE, frameSize, nbBands, lm)

	band := 6
	bandStart := celt.EBands[band] * M
	bandEnd := celt.EBands[band+1] * M
	if bandEnd > frameSize {
		bandEnd = frameSize
	}
	if bandStart < 0 || bandStart >= bandEnd {
		t.Fatalf("invalid band range for band %d", band)
	}

	maxDiff := 0.0
	maxIdx := -1
	for i := bandStart; i < bandEnd; i++ {
		diff := math.Abs(float64(libNorm[i]) - goNorm[i])
		if diff > maxDiff {
			maxDiff = diff
			maxIdx = i
		}
	}

	t.Logf("Band %d range [%d-%d) maxDiff=%.10f at idx %d", band, bandStart, bandEnd, maxDiff, maxIdx)
	t.Log("Index | libNorm | goNorm | diff")
	for i := bandStart; i < bandEnd && i < bandStart+4; i++ {
		diff := float64(libNorm[i]) - goNorm[i]
		t.Logf("%5d | %+0.10f | %+0.10f | %+0.10f", i, libNorm[i], goNorm[i], diff)
	}

	if maxDiff > 1e-6 {
		t.Fatalf("normalized coeffs differ in band %d (maxDiff=%.10f)", band, maxDiff)
	}
}
