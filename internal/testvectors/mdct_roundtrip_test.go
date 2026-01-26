package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestMDCTRoundTrip(t *testing.T) {
	// Simple test: sine wave in, MDCT, IMDCT, check output
	n := 960    // Number of MDCT coefficients (frame size)
	n2 := n * 2 // Input/output size

	// Create input: two halves for overlap-add
	input := make([]float64, n2)
	for i := 0; i < n2; i++ {
		input[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(n2)*10) // 10 cycles
	}

	// Apply Vorbis window to input (like encoder does)
	windowed := make([]float64, n2)
	for i := 0; i < n2; i++ {
		windowed[i] = input[i] * vorbisWindowFull(i, n2)
	}

	// Compute MDCT (using the internal function that also applies window)
	// Let's use the direct function to understand what's happening
	coeffs := make([]float64, n)
	for k := 0; k < n; k++ {
		var sum float64
		kPlus := float64(k) + 0.5
		for ni := 0; ni < n2; ni++ {
			nPlus := float64(ni) + 0.5 + float64(n)/2
			angle := math.Pi / float64(n) * nPlus * kPlus
			sum += windowed[ni] * math.Cos(angle)
		}
		coeffs[k] = sum
	}
	t.Logf("MDCT: input %d samples -> %d coeffs", n2, len(coeffs))
	t.Logf("Input max: %.4f", maxAbsSlice(input))
	t.Logf("Windowed max: %.4f", maxAbsSlice(windowed))
	t.Logf("Coeffs max: %.4f", maxAbsSlice(coeffs))

	// Compute IMDCT
	output := celt.IMDCT(coeffs)
	t.Logf("IMDCT: %d coeffs -> %d samples", len(coeffs), len(output))
	t.Logf("Output max: %.4f", maxAbsSlice(output))

	// Compare middle portion
	t.Log("\nMiddle samples comparison:")
	for i := n / 2; i < n/2+10; i++ {
		t.Logf("  [%d] windowed=%.4f, output=%.4f, ratio=%.4f",
			i, windowed[i], output[i], output[i]/(windowed[i]+1e-10))
	}

	// Check correlation
	var sumXY, sumXX, sumYY float64
	for i := 0; i < n2; i++ {
		sumXY += windowed[i] * output[i]
		sumXX += windowed[i] * windowed[i]
		sumYY += output[i] * output[i]
	}
	corr := sumXY / (math.Sqrt(sumXX*sumYY) + 1e-10)
	t.Logf("\nCorrelation: %.4f", corr)

	// Check if they're negatively correlated
	if corr < -0.9 {
		t.Log("WARNING: Negative correlation - sign inversion in MDCT/IMDCT!")
	}
}

func maxAbsSlice(s []float64) float64 {
	max := 0.0
	for _, v := range s {
		if math.Abs(v) > max {
			max = math.Abs(v)
		}
	}
	return max
}
