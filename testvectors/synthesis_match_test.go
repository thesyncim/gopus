package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestSynthesisMatch verifies that the short-overlap IMDCT path matches
// the encoder MDCT when overlap-add is applied.
func TestSynthesisMatch(t *testing.T) {
	t.Parallel()
	N := 960 // Frame size (MDCT coefficients)
	overlap := 120

	// Create multi-frame signal for overlap-add reconstruction.
	totalFrames := 3
	totalSamples := totalFrames * N
	input := make([]float32, totalSamples)
	for i := range totalSamples {
		input[i] = float32(0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10))
	}

	output := make([]float32, totalSamples)
	history := make([]float32, overlap)
	prevOverlap := make([]float32, overlap)
	for frame := range totalFrames {
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
	t.Parallel()
	N := 960
	overlap := 120

	// Create 3 frames of test signal for proper overlap-add
	totalFrames := 3
	totalSamples := totalFrames * N
	input := make([]float32, totalSamples)
	for i := range totalSamples {
		input[i] = float32(0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10))
	}

	output := make([]float32, totalSamples)
	history := make([]float32, overlap)
	prevOverlap := make([]float32, overlap)

	for frame := range totalFrames {
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
		in := float64(input[i])
		out := float64(output[i])
		sumXY += in * out
		sumYY += out * out
	}
	scale := 1.0
	if sumYY > 0 {
		scale = sumXY / sumYY
	}

	var maxDiff float64
	var signalPower, noisePower float64
	for i := frameStart; i < frameEnd; i++ {
		in := float64(input[i])
		out := float64(output[i])
		diff := math.Abs(in - out*scale)
		if diff > maxDiff {
			maxDiff = diff
		}
		signalPower += in * in
		noise := in - out*scale
		noisePower += noise * noise
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("Max difference: %.6f", maxDiff)
	t.Logf("SNR: %.2f dB (scale=%.6f)", snr, scale)

	// Sample comparison
	t.Log("\nSample comparison at middle of frame 1:")
	midpoint := N + N/2
	for i := midpoint - 5; i <= midpoint+5; i++ {
		in := float64(input[i])
		out := float64(output[i])
		scaled := out * scale
		t.Logf("  [%d] input=%.4f, output=%.4f, diff=%.6f",
			i, in, out, in-scaled)
	}

	// Check if SNR is acceptable
	if snr < 40 {
		t.Errorf("SNR too low: %.2f dB (expected > 40 dB for proper reconstruction)", snr)
	}
}

func correlation[A ~float32 | ~float64, B ~float32 | ~float64](a []A, b []B) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}

	var sumXY, sumXX, sumYY float64
	for i := 0; i < n; i++ {
		av := float64(a[i])
		bv := float64(b[i])
		sumXY += av * bv
		sumXX += av * av
		sumYY += bv * bv
	}

	return sumXY / (math.Sqrt(sumXX*sumYY) + 1e-10)
}
