package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestMDCTShortOverlapRoundTrip tests MDCT/IMDCT with CELT short overlap (120 samples).
// CELT uses short overlap instead of standard 50% overlap.
func TestMDCTShortOverlapRoundTrip(t *testing.T) {
	N := 960 // Frame size (MDCT coefficients)
	overlap := 120

	// Create 3 frames of continuous signal
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

	// Analyze middle frame (frame 1) which has complete overlap-add on both sides
	t.Log("Analyzing middle frame (frame 1):")
	var maxDiff float64
	var signalPower, noisePower float64
	frameStart := N
	frameEnd := 2 * N

	for i := frameStart; i < frameEnd; i++ {
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

	// Sample comparison in middle of frame
	t.Log("\nSample comparison at middle of frame 1:")
	midpoint := N + N/2
	for i := midpoint - 5; i <= midpoint+5; i++ {
		t.Logf("  [%d] signal=%.4f, output=%.4f, diff=%.6f",
			i, signal[i], output[i], signal[i]-output[i])
	}

	// Check edge behavior (overlap region)
	t.Log("\nOverlap region at frame boundary:")
	for i := N - 3; i <= N+3; i++ {
		t.Logf("  [%d] signal=%.4f, output=%.4f, diff=%.6f",
			i, signal[i], output[i], signal[i]-output[i])
	}

	if snr < 40 {
		t.Errorf("SNR too low: %.2f dB (expected > 40 dB for proper short-overlap MDCT)", snr)
	}
}

// TestMDCT50PercentOverlapRoundTrip tests with standard 50% overlap for comparison.
func TestMDCT50PercentOverlapRoundTrip(t *testing.T) {
	N := 960
	N2 := N * 2

	// Create 3 frames
	totalFrames := 3
	totalSamples := totalFrames * N
	signal := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		signal[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(N)*10)
	}

	output := make([]float64, totalSamples)
	prevOverlap := make([]float64, N)

	for frame := 0; frame < totalFrames; frame++ {
		frameInput := make([]float64, N2)

		if frame == 0 {
			copy(frameInput[N:], signal[:N])
		} else {
			copy(frameInput[:N], signal[(frame-1)*N:frame*N])
			copy(frameInput[N:], signal[frame*N:(frame+1)*N])
		}

		// Apply full Vorbis window (50% overlap)
		for i := 0; i < N2; i++ {
			frameInput[i] *= vorbisWindowFull(i, N2)
		}

		// Forward MDCT
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

		// Inverse MDCT
		imdctOut := make([]float64, N2)
		scale := 2.0 / float64(N)
		for n := 0; n < N2; n++ {
			nPlus := float64(n) + 0.5 + float64(N)/2
			var sum float64
			for k := 0; k < N; k++ {
				kPlus := float64(k) + 0.5
				angle := math.Pi / float64(N) * nPlus * kPlus
				sum += coeffs[k] * math.Cos(angle)
			}
			imdctOut[n] = sum * scale
		}

		// Apply window to output
		for i := 0; i < N2; i++ {
			imdctOut[i] *= vorbisWindowFull(i, N2)
		}

		// 50% overlap-add: add first N samples with previous frame's last N
		outStart := frame * N
		for i := 0; i < N; i++ {
			if outStart+i < totalSamples {
				output[outStart+i] = prevOverlap[i] + imdctOut[i]
			}
		}

		// Save second half for next frame
		copy(prevOverlap, imdctOut[N:])
	}

	// Analyze
	var maxDiff float64
	var signalPower, noisePower float64
	for i := N; i < 2*N; i++ {
		diff := math.Abs(signal[i] - output[i])
		if diff > maxDiff {
			maxDiff = diff
		}
		signalPower += signal[i] * signal[i]
		noise := signal[i] - output[i]
		noisePower += noise * noise
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("50%% overlap - Max diff: %.6f, SNR: %.2f dB", maxDiff, snr)

	if snr < 100 {
		t.Errorf("50%% overlap SNR too low: %.2f dB", snr)
	}
}
