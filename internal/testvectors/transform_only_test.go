package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTransformOnlyRoundTrip tests MDCT→IMDCT without any encoding/decoding.
// This isolates the transform from the range coding.
func TestTransformOnlyRoundTrip(t *testing.T) {
	N := 960
	N2 := N * 2

	// Create test signal
	signal := make([]float64, N2)
	for i := 0; i < N2; i++ {
		signal[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10)
	}
	t.Logf("Signal max: %.4f", maxAbsSlice(signal))

	// Forward MDCT using celt.MDCT
	coeffs := celt.MDCT(signal)
	t.Logf("MDCT: %d samples → %d coeffs, max=%.4f", N2, len(coeffs), maxAbsSlice(coeffs))

	// Inverse MDCT using celt.IMDCTDirect
	imdctOut := celt.IMDCTDirect(coeffs)
	t.Logf("IMDCTDirect: %d coeffs → %d samples, max=%.4f", len(coeffs), len(imdctOut), maxAbsSlice(imdctOut))

	// Compare signal with IMDCT output
	corr := correlation(signal, imdctOut)
	t.Logf("Correlation: %.4f", corr)

	// Energy ratio
	energyIn := energy(signal)
	energyOut := energy(imdctOut)
	t.Logf("Energy ratio (out/in): %.4f", energyOut/energyIn)

	// Sample comparison
	t.Log("\nSample comparison (middle of frame):")
	mid := N
	for i := mid - 5; i <= mid+5; i++ {
		t.Logf("  [%d] signal=%.4f, imdct=%.4f, ratio=%.4f",
			i, signal[i], imdctOut[i], imdctOut[i]/(signal[i]+1e-10))
	}

	// Check if IMDCT output matches signal (within scaling factor)
	if math.Abs(corr) < 0.99 {
		t.Errorf("Poor correlation: %.4f (expected > 0.99 or < -0.99)", corr)
	}
}

// TestCELTEncoderBandEnergies tests band energy computation.
func TestCELTEncoderBandEnergies(t *testing.T) {
	N := 960
	N2 := N * 2

	// Create test signal
	signal := make([]float64, N2)
	for i := 0; i < N2; i++ {
		signal[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10)
	}
	t.Logf("Signal max: %.4f", maxAbsSlice(signal))

	// Create encoder
	enc := celt.NewEncoder(1)

	// Apply pre-emphasis (like encoder does)
	preemph := enc.ApplyPreemphasis(signal)
	t.Logf("Preemph max: %.4f", maxAbsSlice(preemph))

	// MDCT
	coeffs := celt.MDCT(preemph)
	t.Logf("MDCT coeffs max: %.4f", maxAbsSlice(coeffs))

	// Compute band energies
	mode := celt.GetModeConfig(N)
	energies := enc.ComputeBandEnergies(coeffs, mode.EffBands, N)
	t.Logf("Band energies computed, first 5: %.2f, %.2f, %.2f, %.2f, %.2f",
		energies[0], energies[1], energies[2], energies[3], energies[4])

	// Normalize bands
	shapes := enc.NormalizeBands(coeffs, energies, mode.EffBands, N)
	t.Logf("Normalized shapes: %d bands", len(shapes))

	// Check if shapes are unit vectors
	t.Log("\nShape L2 norms (should be 1.0 for unit vectors):")
	for b := 0; b < min(5, len(shapes)); b++ {
		var l2 float64
		for _, v := range shapes[b] {
			l2 += v * v
		}
		l2 = math.Sqrt(l2)
		t.Logf("  Band %d: len=%d, L2=%.4f", b, len(shapes[b]), l2)
	}
}

// TestMDCTScaling checks MDCT/IMDCT scaling behavior
func TestMDCTScaling(t *testing.T) {
	N := 960
	N2 := N * 2

	// Create unit impulse at center
	signal := make([]float64, N2)
	signal[N] = 1.0

	// Forward MDCT
	coeffs := celt.MDCT(signal)
	t.Logf("Impulse → MDCT: max coeff = %.4f", maxAbsSlice(coeffs))

	// Sum of absolute coefficients
	var sumAbs float64
	for _, c := range coeffs {
		sumAbs += math.Abs(c)
	}
	t.Logf("Sum of |coeffs| = %.4f", sumAbs)

	// Inverse
	imdctOut := celt.IMDCTDirect(coeffs)
	t.Logf("IMDCT: max output = %.4f", maxAbsSlice(imdctOut))

	// Check reconstruction at center
	t.Logf("Reconstruction at center [%d]: %.4f (should be ~1.0)", N, imdctOut[N])

	// Now test with flat DC signal
	dc := make([]float64, N2)
	for i := range dc {
		dc[i] = 0.5
	}

	coeffsDC := celt.MDCT(dc)
	t.Logf("\nDC signal → MDCT: coeff[0] = %.4f", coeffsDC[0])
	t.Logf("DC signal → MDCT: max coeff = %.4f", maxAbsSlice(coeffsDC))

	imdctDC := celt.IMDCTDirect(coeffsDC)
	t.Logf("DC signal reconstructed: sample[N] = %.4f (should be ~0.5)", imdctDC[N])
}
