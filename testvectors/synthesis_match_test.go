package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestSynthesisMatch verifies that the short-overlap IMDCT path matches
// the encoder MDCT when overlap-add is applied.
func TestSynthesisMatch(t *testing.T) {
	N := 960 // Frame size (MDCT coefficients)
	overlap := 120

	// Create multi-frame signal for overlap-add reconstruction.
	totalFrames := 3
	totalSamples := totalFrames * N
	input := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		input[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10)
	}

	output := make([]float64, totalSamples)
	history := make([]float64, overlap)
	prevOverlap := make([]float64, overlap)
	for frame := 0; frame < totalFrames; frame++ {
		frameSamples := input[frame*N : (frame+1)*N]
		coeffs := celt.ComputeMDCTWithHistory(frameSamples, history, 1)
		imdctOut := celt.IMDCTOverlapWithPrev(coeffs, prevOverlap, overlap)
		overlapWrite(output, imdctOut[:N], frame, N, overlap)
		copy(prevOverlap, imdctOut[N:N+overlap])
	}

	middleStart := N + overlap
	middleEnd := 2*N - overlap
	corr := correlation(input[middleStart:middleEnd], output[middleStart:middleEnd])
	t.Logf("Overlap IMDCT middle correlation: %.4f", corr)
	if corr < 0.99 {
		t.Errorf("Correlation too low: %.4f (expected > 0.99)", corr)
	}
}

// TestEncoderDecoderWithStandardIMDCT tests if using standard IMDCT fixes the amplitude issue.
func TestEncoderDecoderWithStandardIMDCT(t *testing.T) {
	N := 960
	overlap := 120

	// Create 3 frames of test signal for proper overlap-add
	totalFrames := 3
	totalSamples := totalFrames * N
	input := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		input[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10)
	}

	output := make([]float64, totalSamples)
	history := make([]float64, overlap)
	prevOverlap := make([]float64, overlap)

	for frame := 0; frame < totalFrames; frame++ {
		frameSamples := input[frame*N : (frame+1)*N]
		coeffs := celt.ComputeMDCTWithHistory(frameSamples, history, 1)
		imdctOut := celt.IMDCTOverlapWithPrev(coeffs, prevOverlap, overlap)
		overlapWrite(output, imdctOut[:N], frame, N, overlap)
		copy(prevOverlap, imdctOut[N:N+overlap])
	}

	// Compare middle frame (frame 1) which has complete overlap-add
	t.Log("Comparing middle frame (frame 1):")
	var sumXY, sumYY float64
	frameStart := N
	frameEnd := 2 * N

	for i := frameStart; i < frameEnd; i++ {
		sumXY += input[i] * output[i]
		sumYY += output[i] * output[i]
	}
	scale := 1.0
	if sumYY > 0 {
		scale = sumXY / sumYY
	}

	var maxDiff float64
	var signalPower, noisePower float64
	for i := frameStart; i < frameEnd; i++ {
		diff := math.Abs(input[i] - output[i]*scale)
		if diff > maxDiff {
			maxDiff = diff
		}
		signalPower += input[i] * input[i]
		noise := input[i] - output[i]*scale
		noisePower += noise * noise
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("Max difference: %.6f", maxDiff)
	t.Logf("SNR: %.2f dB (scale=%.6f)", snr, scale)

	// Sample comparison
	t.Log("\nSample comparison at middle of frame 1:")
	midpoint := N + N/2
	for i := midpoint - 5; i <= midpoint+5; i++ {
		scaled := output[i] * scale
		t.Logf("  [%d] input=%.4f, output=%.4f, diff=%.6f",
			i, input[i], output[i], input[i]-scaled)
	}

	// Check if SNR is acceptable
	if snr < 40 {
		t.Errorf("SNR too low: %.2f dB (expected > 40 dB for proper reconstruction)", snr)
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
