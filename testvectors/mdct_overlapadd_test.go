package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

func TestMDCTOverlapAdd(t *testing.T) {
	// Test MDCT with proper overlap-add
	n := 960 // MDCT coefficients per frame
	_ = 120  // Overlap size for reference

	// Create longer input (3 frames worth)
	totalSamples := 3 * n
	input := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		input[i] = 0.5 * math.Sin(2*math.Pi*float64(i)/float64(n)*10) // 10 cycles per frame
	}

	// Process 3 frames with overlap-add
	output := make([]float64, totalSamples)

	// For each frame, MDCT takes 2n samples and produces n coefficients
	// IMDCT takes n coefficients and produces 2n samples
	// With overlap-add, we get perfect reconstruction

	// Process frame 1: samples 0 to 2*n (but we only have signal starting at 0)
	// We'll pad with zeros for the first frame
	frame1Input := make([]float64, 2*n)
	copy(frame1Input[n:], input[:n]) // Second half is frame 1

	// Apply window and MDCT
	for i := 0; i < 2*n; i++ {
		frame1Input[i] *= vorbisWindowFull(i, 2*n)
	}
	coeffs1 := mdctDirect(frame1Input, n)
	out1 := celt.IMDCT(coeffs1)
	// Apply window to IMDCT output
	for i := 0; i < len(out1); i++ {
		out1[i] *= vorbisWindowFull(i, 2*n)
	}

	// Write first frame output
	for i := 0; i < n; i++ {
		output[i] = out1[n+i] // Second half of IMDCT output
	}

	// Process frame 2: samples n to 2*n input
	frame2Input := make([]float64, 2*n)
	copy(frame2Input, input[:n])        // First half is frame 1
	copy(frame2Input[n:], input[n:2*n]) // Second half is frame 2
	for i := 0; i < 2*n; i++ {
		frame2Input[i] *= vorbisWindowFull(i, 2*n)
	}
	coeffs2 := mdctDirect(frame2Input, n)
	out2 := celt.IMDCT(coeffs2)
	for i := 0; i < len(out2); i++ {
		out2[i] *= vorbisWindowFull(i, 2*n)
	}

	// Overlap-add frame 2 into output
	// First half of out2 overlaps with second half of out1
	for i := 0; i < n; i++ {
		output[i] += out2[i] // Overlap-add
	}
	for i := n; i < 2*n; i++ {
		output[i] = out2[i]
	}

	// Process frame 3
	frame3Input := make([]float64, 2*n)
	copy(frame3Input, input[n:2*n])
	copy(frame3Input[n:], input[2*n:3*n])
	for i := 0; i < 2*n; i++ {
		frame3Input[i] *= vorbisWindowFull(i, 2*n)
	}
	coeffs3 := mdctDirect(frame3Input, n)
	out3 := celt.IMDCT(coeffs3)
	for i := 0; i < len(out3); i++ {
		out3[i] *= vorbisWindowFull(i, 2*n)
	}

	// Overlap-add
	for i := 0; i < n; i++ {
		output[n+i] += out3[i]
	}
	for i := n; i < 2*n; i++ {
		output[n+i] = out3[i]
	}

	// Compare middle frame (frame 2) which has complete overlap-add
	t.Log("Comparing middle portion (frame 2):")
	var maxDiff float64
	for i := n; i < 2*n; i++ {
		diff := math.Abs(input[i] - output[i])
		if diff > maxDiff {
			maxDiff = diff
		}
	}
	t.Logf("Max difference in frame 2: %.6f", maxDiff)

	// Print some samples
	for i := n + n/2 - 5; i < n+n/2+5; i++ {
		t.Logf("  [%d] input=%.4f, output=%.4f, diff=%.6f", i, input[i], output[i], input[i]-output[i])
	}

	// SNR for middle frame
	var signalPower, noisePower float64
	for i := n; i < 2*n; i++ {
		signalPower += input[i] * input[i]
		noise := input[i] - output[i]
		noisePower += noise * noise
	}
	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("SNR for middle frame: %.2f dB", snr)
}

func mdctDirect(samples []float64, n int) []float64 {
	n2 := len(samples)
	coeffs := make([]float64, n)
	for k := 0; k < n; k++ {
		var sum float64
		kPlus := float64(k) + 0.5
		for ni := 0; ni < n2; ni++ {
			nPlus := float64(ni) + 0.5 + float64(n)/2.0
			angle := math.Pi / float64(n) * nPlus * kPlus
			sum += samples[ni] * math.Cos(angle)
		}
		coeffs[k] = sum
	}
	return coeffs
}
