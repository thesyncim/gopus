// Package testvectors provides encoder tracing for debugging.
package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestEncoderTrace traces through encoding step by step.
func TestEncoderTrace(t *testing.T) {
	// Generate simple 440Hz sine wave, 1 frame (960 samples)
	pcm := make([]float64, 960)
	for i := range pcm {
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}

	t.Log("=== CELT Encoder Trace ===")

	// Create encoder
	enc := celt.NewEncoder(1) // mono

	// Get mode config
	mode := celt.GetModeConfig(960)
	t.Logf("Frame size: 960, LM: %d, EffBands: %d", mode.LM, mode.EffBands)

	// Check for transient
	transient := enc.DetectTransient(pcm, 960)
	t.Logf("Transient: %v", transient)

	// Apply pre-emphasis
	preemph := enc.ApplyPreemphasis(pcm)
	t.Logf("First 5 pre-emphasized samples: %v", preemph[:5])

	// Compute MDCT (simplified - no overlap for first frame)
	mdct := celt.MDCT(append(make([]float64, celt.Overlap), preemph...))
	t.Logf("MDCT length: %d, first 5 coeffs: %v", len(mdct), mdct[:5])

	// Compute band energies
	energies := enc.ComputeBandEnergies(mdct, mode.EffBands, 960)
	t.Logf("Band energies (first 5):")
	for i := 0; i < 5 && i < len(energies); i++ {
		t.Logf("  Band %d: %.3f dB", i, energies[i])
	}

	// Initialize range encoder
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	t.Log("\n=== Range Encoder State ===")
	t.Logf("Initial: rng=0x%08x, val=0x%08x", re.Range(), re.Val())

	// Encode silence = 0
	re.EncodeBit(0, 15)
	t.Logf("After silence=0: rng=0x%08x, val=0x%08x, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode transient = 0
	re.EncodeBit(0, 3)
	t.Logf("After transient=0: rng=0x%08x, val=0x%08x, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode intra = 1 (first frame)
	re.EncodeBit(1, 3)
	t.Logf("After intra=1: rng=0x%08x, val=0x%08x, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode coarse energy
	t.Log("\n=== Coarse Energy Encoding ===")
	t.Logf("Using intra mode, LM=%d", mode.LM)

	// Trace first few bands energy quantization
	beta := 0.15 // BetaIntra constant
	prevBandEnergy := 0.0

	for band := 0; band < 5 && band < mode.EffBands; band++ {
		energy := energies[band]
		pred := prevBandEnergy // alpha=0 for intra mode
		residual := energy - pred
		qi := int(math.Round(residual / celt.DB6))

		t.Logf("Band %d: energy=%.2f, pred=%.2f, residual=%.2f, qi=%d",
			band, energy, pred, residual, qi)

		// Update prev band energy
		q := float64(qi) * celt.DB6
		prevBandEnergy = prevBandEnergy + q - beta*q

		t.Logf("  prevBandEnergy after: %.2f", prevBandEnergy)
	}

	// Finalize and show output
	result := re.Done()
	t.Logf("\n=== Output ===")
	t.Logf("Result: %x (len=%d)", result, len(result))
	if len(result) >= 8 {
		t.Logf("First 8 bytes: %02x %02x %02x %02x %02x %02x %02x %02x",
			result[0], result[1], result[2], result[3],
			result[4], result[5], result[6], result[7])
	} else {
		t.Logf("First %d bytes: %x", len(result), result)
	}
}

// TestCompareEnergyComputation compares energy computation.
func TestCompareEnergyComputation(t *testing.T) {
	// Generate sine wave
	pcm := make([]float64, 960)
	for i := range pcm {
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}

	// Compute MDCT
	mdct := celt.MDCT(append(make([]float64, celt.Overlap), pcm...))

	// Show MDCT statistics
	var sumSq float64
	var maxAbs float64
	for _, v := range mdct {
		sumSq += v * v
		if math.Abs(v) > maxAbs {
			maxAbs = math.Abs(v)
		}
	}
	t.Logf("MDCT: len=%d, sumSq=%.2f, maxAbs=%.4f, RMS=%.4f",
		len(mdct), sumSq, maxAbs, math.Sqrt(sumSq/float64(len(mdct))))

	// Show energy for first few bands
	mode := celt.GetModeConfig(960)
	enc := celt.NewEncoder(1)
	energies := enc.ComputeBandEnergies(mdct, mode.EffBands, 960)

	for i := 0; i < 10 && i < len(energies); i++ {
		start := celt.ScaledBandStart(i, 960)
		end := celt.ScaledBandEnd(i, 960)
		width := end - start

		var bandSumSq float64
		for j := start; j < end && j < len(mdct); j++ {
			bandSumSq += mdct[j] * mdct[j]
		}
		rms := math.Sqrt(bandSumSq / float64(width))
		logRMS := celt.DB6 * 0.5 * math.Log2(bandSumSq/float64(width))

		t.Logf("Band %d: width=%d, sumSq=%.4f, RMS=%.4f, energy=%.2f (computed: %.2f)",
			i, width, bandSumSq, rms, logRMS, energies[i])
	}
}
