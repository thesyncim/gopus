package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestCoarseEnergyEncoding(t *testing.T) {
	sampleRate := 48000
	frameSize := 960

	// Generate simple sine wave
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	enc := celt.NewEncoder(1)
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Apply pre-emphasis
	preemph := enc.ApplyPreemphasis(pcm)

	// Compute MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)

	// Compute band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("=== Input Energies (first 10 bands) ===")
	for i := 0; i < 10 && i < len(energies); i++ {
		t.Logf("  Band %2d: %.4f", i, energies[i])
	}

	// Initialize range encoder
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	enc.SetRangeEncoder(re)

	// Encode coarse energy (intra mode for first frame)
	quantized := enc.EncodeCoarseEnergy(energies, nbBands, true, lm)

	t.Log("\n=== Quantized Energies (first 10 bands) ===")
	for i := 0; i < 10 && i < len(quantized); i++ {
		diff := quantized[i] - energies[i]
		t.Logf("  Band %2d: input=%.4f, quant=%.4f, diff=%.4f", i, energies[i], quantized[i], diff)
	}

	// Get encoded bytes
	tell := re.Tell()
	bytes := re.Done()

	t.Logf("\n=== Encoded Data ===")
	t.Logf("Bits used: %d", tell)
	t.Logf("Bytes: %d", len(bytes))
	t.Logf("First 20 bytes: %02X", bytes[:20])

}
