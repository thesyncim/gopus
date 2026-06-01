package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestTransformMiddleRegion verifies MDCT/IMDCT on the middle region only.
// The middle region is unaffected by short-overlap windowing and should reconstruct perfectly.
func TestTransformMiddleRegion(t *testing.T) {
	t.Parallel()
	N := 960
	overlap := 120

	// Create multi-frame signal for overlap-add reconstruction.
	totalFrames := 3
	totalSamples := totalFrames * N
	signal := make([]float32, totalSamples)
	for i := 0; i < totalSamples; i++ {
		signal[i] = float32(0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10))
	}

	output := make([]float32, totalSamples)
	history := make([]float32, overlap)
	prevOverlap := make([]float32, overlap)
	for frame := 0; frame < totalFrames; frame++ {
		frameSamples := signal[frame*N : (frame+1)*N]
		coeffs := celt.ComputeMDCTWithHistory(frameSamples, history, 1)
		imdctOut := celt.IMDCTOverlapWithPrev(coeffs, prevOverlap, overlap)
		overlapWrite(output, imdctOut[:N], frame, N, overlap)
		copy(prevOverlap, imdctOut[N:N+overlap])
	}

	// Check middle region only (exclude first and last 'overlap' samples)
	// The short overlap window affects samples [0:overlap] and [N-overlap:N]
	// Middle region: [overlap : N-overlap]
	middleStart := N + overlap
	middleEnd := 2*N - overlap

	var sumXY, sumYY float64
	for i := middleStart; i < middleEnd; i++ {
		sig := float64(signal[i])
		out := float64(output[i])
		sumXY += sig * out
		sumYY += out * out
	}
	scale := 1.0
	if sumYY > 0 {
		scale = sumXY / sumYY
	}

	var maxDiff float64
	var signalPower, noisePower float64
	for i := middleStart; i < middleEnd; i++ {
		sig := float64(signal[i])
		out := float64(output[i])
		diff := math.Abs(sig - out*scale)
		if diff > maxDiff {
			maxDiff = diff
		}
		signalPower += sig * sig
		noise := sig - out*scale
		noisePower += noise * noise
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("Middle region [%d:%d] analysis:", middleStart, middleEnd)
	t.Logf("  Max difference: %.6f", maxDiff)
	t.Logf("  SNR: %.2f dB (scale=%.6f)", snr, scale)

	// Correlation for middle region
	var sumXYCorr, sumXX, sumYYCorr float64
	for i := middleStart; i < middleEnd; i++ {
		sig := float64(signal[i])
		out := float64(output[i])
		sumXYCorr += sig * out * scale
		sumXX += sig * sig
		sumYYCorr += out * out * scale * scale
	}
	corr := sumXYCorr / (math.Sqrt(sumXX*sumYYCorr) + 1e-10)
	t.Logf("  Correlation: %.6f", corr)

	// Sample comparison in middle
	t.Log("\nSample comparison in middle region:")
	mid := (middleStart + middleEnd) / 2
	for i := mid - 5; i <= mid+5 && i < len(output); i++ {
		sig := float64(signal[i])
		out := float64(output[i])
		scaled := out * scale
		ratio := scaled / (sig + 1e-10)
		t.Logf("  [%d] signal=%.6f, output=%.6f, scaled=%.6f, ratio=%.6f", i, signal[i], output[i], scaled, ratio)
	}

	// The middle region should have SNR > 100 dB (near-perfect)
	if snr < 100 {
		t.Errorf("Middle region SNR too low: %.2f dB (expected > 100 dB)", snr)
	}
}

// TestMultiFrameOverlapAdd tests proper multi-frame reconstruction with overlap-add.
func TestMultiFrameOverlapAdd(t *testing.T) {
	t.Parallel()
	N := 960
	overlap := 120
	totalFrames := 3
	totalSamples := totalFrames * N

	// Create continuous signal
	signal := make([]float32, totalSamples)
	for i := range signal {
		signal[i] = float32(0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10))
	}

	output := make([]float32, totalSamples)
	history := make([]float32, overlap)
	prevOverlap := make([]float32, overlap)

	for frame := 0; frame < totalFrames; frame++ {
		frameSamples := signal[frame*N : (frame+1)*N]
		coeffs := celt.ComputeMDCTWithHistory(frameSamples, history, 1)
		imdctOut := celt.IMDCTOverlapWithPrev(coeffs, prevOverlap, overlap)
		overlapWrite(output, imdctOut[:N], frame, N, overlap)
		copy(prevOverlap, imdctOut[N:N+overlap])
	}

	// Analyze middle frame (frame 1) which has complete overlap-add
	t.Log("Analyzing middle frame (frame 1):")
	var maxDiff float64
	var signalPower, noisePower float64
	// Skip first 'overlap' samples since they're affected by first frame boundary
	for i := N + overlap; i < 2*N-overlap; i++ {
		sig := float64(signal[i])
		out := float64(output[i])
		diff := math.Abs(sig - out)
		if diff > maxDiff {
			maxDiff = diff
		}
		signalPower += sig * sig
		noise := sig - out
		noisePower += noise * noise
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("Max difference: %.6f", maxDiff)
	t.Logf("SNR: %.2f dB", snr)

	// Sample comparison
	t.Log("\nSample comparison at middle of frame 1:")
	mid := N + N/2
	for i := mid - 3; i <= mid+3; i++ {
		sig := float64(signal[i])
		out := float64(output[i])
		t.Logf("  [%d] signal=%.6f, output=%.6f, diff=%.6f",
			i, sig, out, sig-out)
	}

	// Sample comparison at frame boundary
	t.Log("\nSample comparison at frame boundary (N):")
	for i := N - 2; i <= N+2; i++ {
		sig := float64(signal[i])
		out := float64(output[i])
		t.Logf("  [%d] signal=%.6f, output=%.6f, diff=%.6f",
			i, sig, out, sig-out)
	}
}
