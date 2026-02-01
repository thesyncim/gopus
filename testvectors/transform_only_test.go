package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestTransformOnlyRoundTrip tests MDCT→IMDCT without any encoding/decoding.
// This isolates the transform from the range coding.
func TestTransformOnlyRoundTrip(t *testing.T) {
	N := 960
	overlap := 120

	totalFrames := 3
	totalSamples := totalFrames * N
	signal := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		signal[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10)
	}

	output := make([]float64, totalSamples)
	history := make([]float64, overlap)
	prevOverlap := make([]float64, overlap)

	for frame := 0; frame < totalFrames; frame++ {
		frameSamples := signal[frame*N : (frame+1)*N]
		coeffs := celt.ComputeMDCTWithHistory(frameSamples, history, 1)
		imdctOut := celt.IMDCTOverlapWithPrev(coeffs, prevOverlap, overlap)
		overlapWrite(output, imdctOut[:N], frame, N, overlap)
		copy(prevOverlap, imdctOut[N:N+overlap])
	}

	// Analyze middle frame for best overlap-add quality
	start := N
	end := 2 * N
	var signalPower, noisePower float64
	var sumXY, sumYY float64
	for i := start; i < end; i++ {
		sumXY += signal[i] * output[i]
		sumYY += output[i] * output[i]
	}
	scale := 1.0
	if sumYY > 0 {
		scale = sumXY / sumYY
	}
	for i := start; i < end; i++ {
		signalPower += signal[i] * signal[i]
		diff := signal[i] - output[i]*scale
		noisePower += diff * diff
	}
	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("SNR (middle frame): %.2f dB (scale=%.6f)", snr, scale)
	if snr < 40 {
		t.Errorf("SNR too low: %.2f dB (expected > 40 dB)", snr)
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

	// MDCT with zero history overlap
	history := make([]float64, celt.Overlap)
	coeffs := celt.ComputeMDCTWithHistory(preemph, history, 1)
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
