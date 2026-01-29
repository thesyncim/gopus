package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestVBRDebug(t *testing.T) {
	sampleRate := 48000
	frameSize := 960
	freq := 440.0

	// Generate signal
	pcm := make([]float64, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	// Compute band energies like the encoder does
	enc := celt.NewEncoder(1)
	preemph := enc.ApplyPreemphasis(pcm)

	mode := celt.GetModeConfig(frameSize)
	coeffs := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)
	bandEnergies := enc.ComputeBandEnergies(coeffs, mode.EffBands, frameSize)

	t.Log("Band energies (log dB):")
	for i, e := range bandEnergies {
		t.Logf("  Band %2d: %.2f dB", i, e)
	}

	// Compute maxDepth like DynallocAnalysis does
	lm := mode.LM
	nbBands := mode.EffBands

	noiseFloor := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		// Simplified noise floor computation using constants
		noiseFloor[i] = -96.0 // Assume 16-bit noise floor
	}

	maxDepth := -31.9
	for i := 0; i < nbBands; i++ {
		depth := bandEnergies[i] - noiseFloor[i]
		if depth > maxDepth {
			maxDepth = depth
		}
	}

	t.Logf("\nMaxDepth: %.2f dB", maxDepth)

	// Now trace the VBR floor_depth calculation
	bitrate := 64000
	baseBits := bitrate * frameSize / 48000
	baseTargetQ3 := baseBits << 3 // Convert to Q3

	t.Logf("\nVBR calculation:")
	t.Logf("  baseBits: %d", baseBits)
	t.Logf("  baseTargetQ3: %d (%.1f bits)", baseTargetQ3, float64(baseTargetQ3)/8)

	// Compute floor_depth
	bins := celt.EBands[nbBands-2] << lm
	channels := 1
	floorDepth := int(float64(channels*bins<<3) * maxDepth / 32768.0)

	t.Logf("  bins: %d", bins)
	t.Logf("  floorDepth (raw): %d", floorDepth)

	// The clamping
	if floorDepth < baseTargetQ3/4 {
		t.Logf("  floorDepth clamped to target/4: %d", baseTargetQ3/4)
		floorDepth = baseTargetQ3 / 4
	}

	t.Logf("  Final floorDepth: %d (%.1f bits)", floorDepth, float64(floorDepth)/8)
	t.Logf("  Would clamp target? %v", baseTargetQ3 > floorDepth)
	if baseTargetQ3 > floorDepth {
		t.Logf("  Target would be reduced from %.1f to %.1f bits!",
			float64(baseTargetQ3)/8, float64(floorDepth)/8)
	}
}

func computeNoiseFloorSimple(band, lsbDepth int, logN int16) float64 {
	// Simplified noise floor: assume flat noise at -96 dB for 16-bit
	// Reference: libopus celt_encoder.c line 1062
	return -float64(lsbDepth*6) + float64(logN)/256.0
}
