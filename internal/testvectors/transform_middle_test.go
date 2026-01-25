package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTransformMiddleRegion verifies MDCT/IMDCT on the middle region only.
// The middle region is unaffected by short-overlap windowing and should reconstruct perfectly.
func TestTransformMiddleRegion(t *testing.T) {
	N := 960
	N2 := N * 2
	overlap := 120

	// Create test signal
	signal := make([]float64, N2)
	for i := 0; i < N2; i++ {
		signal[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10)
	}

	// Forward MDCT (applies short-overlap window internally)
	coeffs := celt.MDCT(signal)

	// Inverse MDCT
	imdctOut := celt.IMDCTDirect(coeffs)

	// Check middle region only (exclude first and last 'overlap' samples)
	// The short overlap window affects samples [0:overlap] and [N2-overlap:N2]
	// Middle region: [overlap : N2-overlap] = [120 : 1800]
	middleStart := overlap
	middleEnd := N2 - overlap

	var maxDiff float64
	var signalPower, noisePower float64
	for i := middleStart; i < middleEnd; i++ {
		diff := math.Abs(signal[i] - imdctOut[i])
		if diff > maxDiff {
			maxDiff = diff
		}
		signalPower += signal[i] * signal[i]
		noise := signal[i] - imdctOut[i]
		noisePower += noise * noise
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("Middle region [%d:%d] analysis:", middleStart, middleEnd)
	t.Logf("  Max difference: %.6f", maxDiff)
	t.Logf("  SNR: %.2f dB", snr)

	// Correlation for middle region
	var sumXY, sumXX, sumYY float64
	for i := middleStart; i < middleEnd; i++ {
		sumXY += signal[i] * imdctOut[i]
		sumXX += signal[i] * signal[i]
		sumYY += imdctOut[i] * imdctOut[i]
	}
	corr := sumXY / (math.Sqrt(sumXX*sumYY) + 1e-10)
	t.Logf("  Correlation: %.6f", corr)

	// Sample comparison in middle
	t.Log("\nSample comparison in middle region:")
	mid := N
	for i := mid - 5; i <= mid+5; i++ {
		ratio := imdctOut[i] / (signal[i] + 1e-10)
		t.Logf("  [%d] signal=%.6f, imdct=%.6f, ratio=%.6f", i, signal[i], imdctOut[i], ratio)
	}

	// Check edge behavior
	t.Log("\nEdge region samples (should be windowed down):")
	t.Log("  First edge [0:5]:")
	for i := 0; i < 5; i++ {
		window := celt.GetWindowBuffer(overlap)
		t.Logf("    [%d] signal=%.6f, imdct=%.6f, window=%.6f, expected=%.6f",
			i, signal[i], imdctOut[i], window[i], signal[i]*window[i]*window[i])
	}

	// The middle region should have SNR > 100 dB (near-perfect)
	if snr < 100 {
		t.Errorf("Middle region SNR too low: %.2f dB (expected > 100 dB)", snr)
	}
}

// TestMultiFrameOverlapAdd tests proper multi-frame reconstruction with overlap-add.
func TestMultiFrameOverlapAdd(t *testing.T) {
	N := 960
	N2 := N * 2
	overlap := 120
	totalFrames := 3
	totalSamples := totalFrames * N

	// Create continuous signal
	signal := make([]float64, totalSamples+N) // Extra N for last frame overlap
	for i := range signal {
		signal[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10)
	}

	output := make([]float64, totalSamples)
	prevImdct := make([]float64, N2) // Previous frame's IMDCT output

	window := celt.GetWindowBuffer(overlap)

	for frame := 0; frame < totalFrames; frame++ {
		// Build frame input (2N samples centered around current frame)
		frameInput := make([]float64, N2)
		frameStart := frame * N
		copy(frameInput, signal[frameStart:frameStart+N2])

		// Apply short-overlap window
		for i := 0; i < overlap; i++ {
			frameInput[i] *= window[i]
		}
		for i := 0; i < overlap; i++ {
			idx := N2 - overlap + i
			frameInput[idx] *= window[overlap-1-i]
		}

		// MDCT (without internal windowing since we already applied it)
		// Actually, our MDCT applies windowing internally, so we need to use the raw MDCT
		// Let me compute MDCT directly
		coeffs := make([]float64, N)
		for k := 0; k < N; k++ {
			var sum float64
			kPlus := float64(k) + 0.5
			for n := 0; n < N2; n++ {
				nPlus := float64(n) + 0.5 + float64(N)/2
				angle := math.Pi / float64(N) * nPlus * kPlus
				sum += frameInput[n] * math.Cos(angle)
			}
			coeffs[k] = sum
		}

		// IMDCT
		imdctOut := celt.IMDCTDirect(coeffs)

		// Apply synthesis window
		for i := 0; i < overlap; i++ {
			imdctOut[i] *= window[i]
		}
		for i := 0; i < overlap; i++ {
			idx := N2 - overlap + i
			imdctOut[idx] *= window[overlap-1-i]
		}

		// Overlap-add with previous frame
		outStart := frame * N
		if frame > 0 {
			// Add overlap from previous frame's tail
			for i := 0; i < overlap; i++ {
				output[outStart+i] = prevImdct[N+i] + imdctOut[i]
			}
			// Copy rest from current frame
			for i := overlap; i < N; i++ {
				output[outStart+i] = imdctOut[i]
			}
		} else {
			// First frame - just copy (no previous overlap)
			for i := 0; i < N; i++ {
				output[outStart+i] = imdctOut[i]
			}
		}

		// Save current IMDCT for next frame
		copy(prevImdct, imdctOut)
	}

	// Analyze middle frame (frame 1) which has complete overlap-add
	t.Log("Analyzing middle frame (frame 1):")
	var maxDiff float64
	var signalPower, noisePower float64
	// Skip first 'overlap' samples since they're affected by first frame boundary
	for i := N + overlap; i < 2*N-overlap; i++ {
		diff := math.Abs(signal[i] - output[i])
		if diff > maxDiff {
			maxDiff = diff
		}
		signalPower += signal[i] * signal[i]
		noise := signal[i] - output[i]
		noisePower += noise * noise
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("Max difference: %.6f", maxDiff)
	t.Logf("SNR: %.2f dB", snr)

	// Sample comparison
	t.Log("\nSample comparison at middle of frame 1:")
	mid := N + N/2
	for i := mid - 3; i <= mid+3; i++ {
		t.Logf("  [%d] signal=%.6f, output=%.6f, diff=%.6f",
			i, signal[i], output[i], signal[i]-output[i])
	}

	// Sample comparison at frame boundary
	t.Log("\nSample comparison at frame boundary (N):")
	for i := N - 2; i <= N+2; i++ {
		t.Logf("  [%d] signal=%.6f, output=%.6f, diff=%.6f",
			i, signal[i], output[i], signal[i]-output[i])
	}
}
