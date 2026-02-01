// Package cgo traces MDCT interleaving in transient mode
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTraceMDCTInterleaving traces MDCT coefficient interleaving in transient mode.
func TestTraceMDCTInterleaving(t *testing.T) {
	frameSize := 960
	sampleRate := 48000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	mode := celt.GetModeConfig(frameSize)
	lm := mode.LM
	shortBlocks := mode.ShortBlocks
	M := 1 << lm // M=8 for 960-sample frame

	t.Logf("Frame size: %d, LM: %d, ShortBlocks: %d, M=%d", frameSize, lm, shortBlocks, M)

	// Gopus pipeline with overlap buffer
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(64000)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Apply pre-emphasis with scaling
	gopusPreemph := goEnc.ApplyPreemphasisWithScaling(pcm64)

	// Compute MDCT - this is where interleaving happens for transient mode
	gopusMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, goEnc.OverlapBuffer(), shortBlocks)

	t.Logf("MDCT length: %d (expected %d)", len(gopusMDCT), frameSize)
	t.Logf("ShortBlocks: %d", shortBlocks)

	// In transient mode with shortBlocks=8:
	// - Each short MDCT has frameSize/8 = 120 coefficients
	// - Coefficients are interleaved: outIdx = block + coeffIdx*shortBlocks
	// - Band 0 spans EBands[0]*M to EBands[1]*M = 0*8 to 1*8 = 0 to 8

	// Band 0 indices:
	bandStart := celt.EBands[0] * M // 0
	bandEnd := celt.EBands[1] * M   // 8
	t.Logf("Band 0: indices %d to %d", bandStart, bandEnd)

	t.Log("\n=== Band 0 MDCT Coefficients (interleaved) ===")
	t.Log("Index | MDCT coeff | Block | Freq bin within block")
	for i := bandStart; i < bandEnd; i++ {
		// With interleaving: index i corresponds to block=(i mod 8), freqBin=(i/8)
		block := i % shortBlocks
		freqBin := i / shortBlocks
		t.Logf("%5d | %+12.6f | block=%d | freqBin=%d", i, gopusMDCT[i], block, freqBin)
	}

	// The issue might be: libopus expects coefficients in a different order
	// Let's check what coefficients would be at band 0 if NOT interleaved
	t.Log("\n=== What band 0 would be WITHOUT interleaving ===")
	t.Log("(First 8 coefficients from block 0)")
	for i := 0; i < 8; i++ {
		// In non-interleaved layout, block 0 would have coeffs 0-119
		t.Logf("Block 0, freqBin %d: coeff index would be %d", i, i)
	}

	// Let's also check the mapping:
	// With interleaving ON (what gopus does):
	//   - Band 0 contains samples from ALL 8 blocks at freqBin 0
	//   - index 0 = block 0, freqBin 0
	//   - index 1 = block 1, freqBin 0
	//   - ...
	//   - index 7 = block 7, freqBin 0
	// With interleaving OFF (what might be expected):
	//   - Band 0 contains samples from block 0, freqBins 0-7
	//   - index 0 = block 0, freqBin 0
	//   - index 1 = block 0, freqBin 1
	//   - ...
	//   - index 7 = block 0, freqBin 7

	// Try de-interleaving and see if it makes more sense
	t.Log("\n=== De-interleaved band 0 (block 0, freqBins 0-7) ===")
	for freqBin := 0; freqBin < 8; freqBin++ {
		block := 0
		interleavedIdx := block + freqBin*shortBlocks
		t.Logf("freqBin %d: interleaved index = %d, value = %+12.6f", freqBin, interleavedIdx, gopusMDCT[interleavedIdx])
	}

	// Check band energies
	t.Log("\n=== Band 0 Energy Analysis ===")
	sum := 0.0
	for i := bandStart; i < bandEnd; i++ {
		sum += gopusMDCT[i] * gopusMDCT[i]
	}
	amplitude := math.Sqrt(sum)
	t.Logf("Band 0 L2 amplitude: %.6f", amplitude)

	// Normalize
	t.Log("\n=== Normalized band 0 ===")
	for i := bandStart; i < bandEnd; i++ {
		norm := gopusMDCT[i] / amplitude
		t.Logf("index %d: %.10f", i, norm)
	}
}
