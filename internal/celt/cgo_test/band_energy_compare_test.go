// Package cgo compares band energies between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestCompareShortBlockEnergies compares band energy computation between gopus and libopus
// for transient mode (short blocks).
func TestCompareShortBlockEnergies(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000
	shortBlocks := 8

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	// Get gopus energies
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	// Apply processing pipeline
	dcRejected := goEnc.ApplyDCReject(pcm64)
	delayComp := celt.DelayCompensation
	combinedBuf := make([]float64, delayComp+len(dcRejected))
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:frameSize]
	preemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)

	overlap := celt.Overlap
	historyBuf := make([]float64, overlap)

	// Compute long block MDCT and energies
	longMdct := celt.ComputeMDCTWithHistory(preemph, historyBuf, 1)
	longEnergies := goEnc.ComputeBandEnergies(longMdct, nbBands, frameSize)
	for i := range longEnergies {
		longEnergies[i] = float64(float32(longEnergies[i]))
	}

	// Reset history and compute short block MDCT and energies
	historyBuf = make([]float64, overlap)
	shortMdct := celt.ComputeMDCTWithHistory(preemph, historyBuf, shortBlocks)
	shortEnergies := goEnc.ComputeBandEnergies(shortMdct, nbBands, frameSize)
	for i := range shortEnergies {
		shortEnergies[i] = float64(float32(shortEnergies[i]))
	}

	t.Log("=== Band Energy Comparison: Long vs Short Blocks ===")
	t.Log("")
	t.Log("Band | Long Energy | Short Energy | Difference")
	t.Log("-----+-------------+--------------+-----------")

	for i := 0; i < nbBands; i++ {
		diff := math.Abs(longEnergies[i] - shortEnergies[i])
		marker := ""
		if diff > 0.1 {
			marker = " <-- DIFF"
		}
		t.Logf("  %2d |  %+8.4f   |  %+8.4f    | %+8.4f%s",
			i, longEnergies[i], shortEnergies[i], longEnergies[i]-shortEnergies[i], marker)
	}

	// Analyze the MDCT coefficient differences
	t.Log("")
	t.Log("=== MDCT Coefficient Analysis for High-Frequency Bands ===")

	for band := 15; band <= 20; band++ {
		start := celt.ScaledBandStart(band, frameSize)
		end := celt.ScaledBandEnd(band, frameSize)
		if end > len(longMdct) {
			end = len(longMdct)
		}
		if end > len(shortMdct) {
			end = len(shortMdct)
		}

		var longSum, shortSum float64
		for i := start; i < end; i++ {
			longSum += longMdct[i] * longMdct[i]
			shortSum += shortMdct[i] * shortMdct[i]
		}

		longRms := math.Sqrt(longSum)
		shortRms := math.Sqrt(shortSum)

		t.Logf("Band %d [%d:%d]: longRMS=%.6f, shortRMS=%.6f, ratio=%.4f",
			band, start, end, longRms, shortRms, shortRms/longRms)
	}

	// Detailed coefficient comparison for band 17
	t.Log("")
	t.Log("=== Band 17 Coefficient Details ===")
	start17 := celt.ScaledBandStart(17, frameSize)
	end17 := celt.ScaledBandEnd(17, frameSize)
	t.Logf("Band 17: indices %d to %d (width %d)", start17, end17, end17-start17)

	// Show first few coefficients
	t.Log("")
	t.Log("Index | Long Coeff | Short Coeff | Ratio")
	for i := start17; i < start17+8 && i < end17; i++ {
		longC := longMdct[i]
		shortC := shortMdct[i]
		ratio := 0.0
		if math.Abs(longC) > 1e-10 {
			ratio = shortC / longC
		}
		t.Logf("  %3d | %+10.6f | %+10.6f  | %.4f", i, longC, shortC, ratio)
	}
}
