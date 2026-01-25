package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestSynthesisMatch verifies which IMDCT variant matches the encoder MDCT.
// The encoder uses the standard MDCT formula, so we need to verify
// that the decoder uses a compatible IMDCT.
func TestSynthesisMatch(t *testing.T) {
	N := 960 // Frame size (MDCT coefficients)
	N2 := N * 2

	// Create test signal
	input := make([]float64, N2)
	for i := 0; i < N2; i++ {
		input[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(N2)*10)
	}

	// Encoder MDCT: 2N samples → N coefficients
	coeffs := celt.MDCT(input)
	t.Logf("MDCT: %d samples → %d coefficients", N2, len(coeffs))

	// Test 1: Standard IMDCT (should match)
	imdctStandard := celt.IMDCT(coeffs)
	t.Logf("Standard IMDCT: %d coefficients → %d samples", len(coeffs), len(imdctStandard))

	// Test 2: IMDCTOverlap (what decoder uses)
	overlap := 120
	imdctOverlap := celt.IMDCTOverlap(coeffs, overlap)
	t.Logf("IMDCTOverlap: %d coefficients → %d samples", len(coeffs), len(imdctOverlap))

	// Test 3: IMDCTDirect (should match standard)
	imdctDirect := celt.IMDCTDirect(coeffs)
	t.Logf("IMDCTDirect: %d coefficients → %d samples", len(coeffs), len(imdctDirect))

	// Apply window to input for comparison
	windowed := make([]float64, N2)
	for i := 0; i < N2; i++ {
		windowed[i] = input[i] * celt.VorbisWindow(i, N2)
	}

	// Check correlation with windowed input for each IMDCT variant
	t.Log("\n=== Comparing IMDCT variants to windowed input ===")

	// Standard IMDCT correlation
	corrStandard := correlation(windowed, imdctStandard)
	t.Logf("Standard IMDCT correlation: %.4f", corrStandard)

	// IMDCTDirect correlation
	corrDirect := correlation(windowed, imdctDirect)
	t.Logf("IMDCTDirect correlation: %.4f", corrDirect)

	// IMDCTOverlap correlation (compare middle portion)
	// IMDCTOverlap outputs N+overlap samples, so we need to align
	overlapOut := make([]float64, N2)
	if len(imdctOverlap) >= N2 {
		copy(overlapOut, imdctOverlap[:N2])
	} else {
		copy(overlapOut, imdctOverlap)
	}
	corrOverlap := correlation(windowed, overlapOut)
	t.Logf("IMDCTOverlap correlation: %.4f", corrOverlap)

	// Check energy ratio
	energyWindowed := energy(windowed)
	energyStandard := energy(imdctStandard)
	energyDirect := energy(imdctDirect)
	energyOverlap := energy(imdctOverlap)

	t.Logf("\nEnergy ratios (vs windowed input):")
	t.Logf("  Standard IMDCT: %.4f", energyStandard/energyWindowed)
	t.Logf("  IMDCTDirect: %.4f", energyDirect/energyWindowed)
	t.Logf("  IMDCTOverlap: %.4f", energyOverlap/energyWindowed)

	// Sample comparison
	t.Log("\nSample comparison at midpoint:")
	midpoint := N
	for i := midpoint - 3; i <= midpoint+3; i++ {
		t.Logf("  [%d] windowed=%.4f, standard=%.4f, overlap=%.4f",
			i, windowed[i], imdctStandard[i], safeIndex(imdctOverlap, i))
	}

	// The standard IMDCT should produce windowed input scaled by some factor
	// Let's check the ratio
	if len(imdctStandard) >= N2 && imdctStandard[midpoint] != 0 {
		ratio := windowed[midpoint] / imdctStandard[midpoint]
		t.Logf("\nRatio windowed/standard at midpoint: %.4f", ratio)
	}
}

// TestEncoderDecoderWithStandardIMDCT tests if using standard IMDCT fixes the amplitude issue.
func TestEncoderDecoderWithStandardIMDCT(t *testing.T) {
	N := 960
	N2 := N * 2
	overlap := 120

	// Create 3 frames of test signal for proper overlap-add
	totalFrames := 3
	totalSamples := totalFrames * N
	input := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		input[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10)
	}

	output := make([]float64, totalSamples)
	prevOverlap := make([]float64, overlap)

	for frame := 0; frame < totalFrames; frame++ {
		// Prepare frame input with overlap from previous frame
		frameInput := make([]float64, N2)

		if frame == 0 {
			// First frame: zeros in first half
			copy(frameInput[N:], input[:N])
		} else {
			// Copy previous frame's second half and current frame's first half
			startPrev := (frame - 1) * N
			startCurr := frame * N
			copy(frameInput, input[startPrev:startPrev+N])
			copy(frameInput[N:], input[startCurr:startCurr+N])
		}

		// Window the input
		for i := 0; i < N2; i++ {
			frameInput[i] *= celt.VorbisWindow(i, N2)
		}

		// Encoder MDCT
		coeffs := celt.MDCT(frameInput)

		// Decoder IMDCT (standard, NOT IMDCTOverlap)
		imdctOut := celt.IMDCTDirect(coeffs)

		// Apply synthesis window
		for i := 0; i < len(imdctOut); i++ {
			imdctOut[i] *= celt.VorbisWindow(i, N2)
		}

		// Overlap-add
		outStart := frame * N

		// Add overlap region
		for i := 0; i < overlap && i < len(prevOverlap); i++ {
			if outStart+i < totalSamples {
				output[outStart+i] = prevOverlap[i] + imdctOut[i]
			}
		}

		// Copy non-overlap region
		for i := overlap; i < N; i++ {
			if outStart+i < totalSamples {
				output[outStart+i] = imdctOut[i]
			}
		}

		// Save overlap for next frame
		copy(prevOverlap, imdctOut[N:N+overlap])
	}

	// Compare middle frame (frame 1) which has complete overlap-add
	t.Log("Comparing middle frame (frame 1):")
	var maxDiff float64
	var signalPower, noisePower float64
	frameStart := N
	frameEnd := 2 * N

	for i := frameStart; i < frameEnd; i++ {
		diff := math.Abs(input[i] - output[i])
		if diff > maxDiff {
			maxDiff = diff
		}
		signalPower += input[i] * input[i]
		noise := input[i] - output[i]
		noisePower += noise * noise
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("Max difference: %.6f", maxDiff)
	t.Logf("SNR: %.2f dB", snr)

	// Sample comparison
	t.Log("\nSample comparison at middle of frame 1:")
	midpoint := N + N/2
	for i := midpoint - 5; i <= midpoint+5; i++ {
		t.Logf("  [%d] input=%.4f, output=%.4f, diff=%.6f",
			i, input[i], output[i], input[i]-output[i])
	}

	// Check if SNR is acceptable
	if snr < 50 {
		t.Errorf("SNR too low: %.2f dB (expected > 50 dB for proper reconstruction)", snr)
	}
}

func correlation(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}

	var sumXY, sumXX, sumYY float64
	for i := 0; i < n; i++ {
		sumXY += a[i] * b[i]
		sumXX += a[i] * a[i]
		sumYY += b[i] * b[i]
	}

	return sumXY / (math.Sqrt(sumXX*sumYY) + 1e-10)
}

func energy(s []float64) float64 {
	var sum float64
	for _, v := range s {
		sum += v * v
	}
	return sum
}

func safeIndex(s []float64, i int) float64 {
	if i >= 0 && i < len(s) {
		return s[i]
	}
	return 0
}
