//go:build trace
// +build trace

// Temporary debug test to compare bandLogE/bandLogE2 and follower for band 2.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

func TestTmpBand2DynallocCompare(t *testing.T) {
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
	overlap := celt.Overlap

	// GOPUS pipeline (DC reject + delay + preemph + MDCT energies)
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Apply DC reject (matches encoder path)
	dcRejected := goEnc.ApplyDCReject(pcm64)
	// Apply delay compensation (192 samples of lookahead)
	delayComp := 192
	combined := make([]float64, delayComp+len(dcRejected))
	copy(combined[delayComp:], dcRejected)
	samplesForFrame := combined[:frameSize]

	gopusPreemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)

	gopusShortMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, make([]float64, overlap), shortBlocks)
	gopusBandLogE := goEnc.ComputeBandEnergies(gopusShortMDCT, nbBands, frameSize)
	roundToFloat32(gopusBandLogE)

	gopusLongMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, make([]float64, overlap), 1)
	gopusBandLogE2 := goEnc.ComputeBandEnergies(gopusLongMDCT, nbBands, frameSize)
	roundToFloat32(gopusBandLogE2)
	// Add offset like libopus for bandLogE2
	offset := 0.5 * float64(lm)
	for i := range gopusBandLogE2 {
		gopusBandLogE2[i] += offset
	}
	roundToFloat32(gopusBandLogE2)

	// LIBOPUS pipeline using MDCT wrapper (same DC reject + delay)
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
	libInput := make([]float32, frameSize+overlap)
	copy(libInput[overlap:], libPreemph)

	// Short-block MDCT (shift=3) and interleave
	shortSize := frameSize / shortBlocks
	libShortMDCT := make([]float32, frameSize)
	for b := 0; b < shortBlocks; b++ {
		blockStart := b * shortSize
		blockInput := make([]float32, shortSize+overlap)
		copy(blockInput, libInput[blockStart:blockStart+shortSize+overlap])
		blockMDCT := modeLib.MDCTForward(blockInput, 3)
		for i, v := range blockMDCT {
			outIdx := b + i*shortBlocks
			if outIdx < len(libShortMDCT) {
				libShortMDCT[outIdx] = v
			}
		}
	}
	libBandLogE := ComputeLibopusBandEnergies(libShortMDCT, nbBands, frameSize, lm)

	// Long MDCT (shift=0)
	libLongMDCT := modeLib.MDCTForward(libInput, 0)
	libBandLogE2 := ComputeLibopusBandEnergies(libLongMDCT, nbBands, frameSize, lm)
	// Add offset in float32
	libOffset := float32(0.5 * float64(lm))
	for i := range libBandLogE2 {
		libBandLogE2[i] += libOffset
	}

	// Convert libopus energies to float64
	libBandLogE64 := make([]float64, nbBands)
	libBandLogE2_64 := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		libBandLogE64[i] = float64(libBandLogE[i])
		libBandLogE2_64[i] = float64(libBandLogE2[i])
	}

	// Trace dynalloc follower for both sets
	lsbDepth := 24
	effectiveBytes := 159
	isTransient := true
	traceGo := TraceDynallocAnalysis(gopusBandLogE, gopusBandLogE2, nbBands, 0, nbBands, 1, lsbDepth, lm, effectiveBytes, isTransient, false, false)
	traceLib := TraceDynallocAnalysis(libBandLogE64, libBandLogE2_64, nbBands, 0, nbBands, 1, lsbDepth, lm, effectiveBytes, isTransient, false, false)

	band := 2
	quanta := celt.ScaledBandWidth(band, 120<<lm) << 3
	if quanta < (6 << 3) {
		quanta = 6 << 3
	}

	t.Logf("Band %d energies:", band)
	t.Logf("  gopus bandLogE=%.8f bandLogE2=%.8f", gopusBandLogE[band], gopusBandLogE2[band])
	t.Logf("  lib   bandLogE=%.8f bandLogE2=%.8f", libBandLogE64[band], libBandLogE2_64[band])
	t.Logf("  diff  bandLogE=%.8f bandLogE2=%.8f", gopusBandLogE[band]-libBandLogE64[band], gopusBandLogE2[band]-libBandLogE2_64[band])

	t.Logf("Band %d follower final:", band)
	t.Logf("  gopus follower=%.8f", traceGo.FollowerFinal[band])
	t.Logf("  lib   follower=%.8f", traceLib.FollowerFinal[band])
	t.Logf("  diff  follower=%.8f", traceGo.FollowerFinal[band]-traceLib.FollowerFinal[band])

	t.Logf("Band %d boost (count): gopus=%d lib=%d", band, traceGo.Boost[band], traceLib.Boost[band])
	t.Logf("Band %d boost_bits (Q3): gopus=%d lib=%d (quanta=%d)", band, traceGo.BoostBits[band], traceLib.BoostBits[band], quanta)
	// Show nearby bands for context
	for i := band - 1; i <= band+1; i++ {
		if i < 0 || i >= nbBands {
			continue
		}
		t.Logf("Band %d follower: go=%.8f lib=%.8f", i, traceGo.FollowerFinal[i], traceLib.FollowerFinal[i])
	}
}

func roundToFloat32(x []float64) {
	for i, v := range x {
		x[i] = float64(float32(v))
	}
}
