package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestPVQSignTrace(t *testing.T) {
	frameSize := 960

	// Generate sine wave
	pcm := make([]float64, frameSize)
	for i := range pcm {
		ti := float64(i) / 48000.0
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== PVQ Sign Tracing ===")

	// Get MDCT coefficients
	enc := celt.NewEncoder(1)
	preemph := enc.ApplyPreemphasisWithScaling(pcm)
	mdct := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)

	t.Log("\nMDCT coefficients in band 2 (440Hz region):")
	mode := celt.GetModeConfig(frameSize)
	lm := mode.LM
	band2Start := celt.EBands[2] << lm
	band2End := celt.EBands[3] << lm
	for i := band2Start; i < band2End && i < len(mdct); i++ {
		t.Logf("  MDCT[%d] = %+.4f", i, mdct[i])
	}

	// Compute band energies
	nbBands := mode.EffBands
	energies := enc.ComputeBandEnergies(mdct, nbBands, frameSize)
	t.Logf("\nBand 2 energy: %.4f", energies[2])

	// Normalize band 2
	t.Log("\nNormalizing band 2:")
	band2Coeffs := mdct[band2Start:band2End]

	// Get gain
	eVal := energies[2]
	if 2 < len(celt.EMeans) {
		eVal += celt.EMeans[2] * celt.DB6
	}
	gain := math.Exp2(eVal / celt.DB6)
	t.Logf("  Energy: %.4f, Gain: %.4f", energies[2], gain)

	// Normalize
	normalized := make([]float64, len(band2Coeffs))
	for i, c := range band2Coeffs {
		normalized[i] = c / gain
		t.Logf("  Norm[%d] = %.6f (from coeff %.4f)", i, normalized[i], c)
	}

	// Compute L2 norm of normalized
	var l2Norm float64
	for _, v := range normalized {
		l2Norm += v * v
	}
	l2Norm = math.Sqrt(l2Norm)
	t.Logf("\n  L2 norm of normalized: %.6f (should be close to 1)", l2Norm)

	// Now unit-normalize for PVQ
	t.Log("\nUnit-normalizing for PVQ:")
	unitNorm := make([]float64, len(normalized))
	for i := range normalized {
		unitNorm[i] = normalized[i] / l2Norm
		t.Logf("  UnitNorm[%d] = %+.6f", i, unitNorm[i])
	}

	// Test PVQ encoding with local function
	t.Log("\nPVQ pulse quantization (K=5):")
	pulses := vectorToPulsesLocal(unitNorm, 5)
	t.Logf("Pulses: %v", pulses)

	// Check if signs match
	t.Log("\nSign comparison:")
	for i := range pulses {
		normSign := "+"
		if unitNorm[i] < 0 {
			normSign = "-"
		}
		pulseSign := "+"
		if pulses[i] < 0 {
			pulseSign = "-"
		}
		match := "MATCH"
		if (unitNorm[i] < 0) != (pulses[i] < 0) {
			if pulses[i] != 0 && math.Abs(unitNorm[i]) > 0.01 {
				match = "MISMATCH!"
			}
		}
		if pulses[i] == 0 {
			match = "(zero)"
		}
		absPulse := pulses[i]
		if absPulse < 0 {
			absPulse = -absPulse
		}
		t.Logf("  [%d]: norm=%s%.4f, pulse=%s%d - %s",
			i, normSign, math.Abs(unitNorm[i]), pulseSign, absPulse, match)
	}
}
