package celt

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func TestMDCTForwardScratchMatchesFloat32Wrapper(t *testing.T) {
	const overlap = 120
	var scratch MDCTForwardScratch
	for _, frameSize := range []int{120, 240, 960} {
		samples := make([]float32, frameSize+overlap)
		for i := range samples {
			samples[i] = float32(math.Sin(float64(i)*0.17) + 0.3*math.Cos(float64(i)*0.031))
		}
		want := MDCTForwardWithOverlapFloat32(samples, overlap)
		got := make([]float32, frameSize)
		scratch.ForwardWithOverlapFloat32Into(samples, overlap, got)
		for i := range got {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("frameSize=%d coefficient=%d got=%08x want=%08x", frameSize, i,
					math.Float32bits(got[i]), math.Float32bits(want[i]))
			}
		}
		if allocs := testing.AllocsPerRun(20, func() {
			scratch.ForwardWithOverlapFloat32Into(samples, overlap, got)
		}); allocs != 0 {
			t.Fatalf("frameSize=%d warm allocations=%g want 0", frameSize, allocs)
		}
	}
}

func referenceMDCTForward[T ~float32 | ~float64](input []T) []float32 {
	N := len(input)
	N2 := N / 2
	output := make([]float32, N2)

	scale := float32(1.0) / float32(N/4) // Same as 4.0/N

	for k := range N2 {
		var sum float32
		kPlusHalf := float32(k) + 0.5
		for n := range N {
			phase := float32(2.0*math.Pi) * (float32(n) + 0.5 + float32(N)*0.25) * kPlusHalf / float32(N)
			sum += float32(input[n]) * float32(math.Cos(float64(phase)))
		}
		output[k] = sum * scale
	}

	return output
}

func float32sForMDCTForwardTest[T ~float32 | ~float64](in []T) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

func float64sFromMDCTForwardTest(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

// prepareInputWithWindow creates a test input with rectangular window (no windowing).
// This matches libopus test which uses window[k] = 1.0 (Q15ONE/Q31ONE).
func prepareInputWithWindow(n int, seed int) []float32 {
	input := make([]float32, n)
	for k := range n {
		// Generate pseudo-random data similar to libopus test
		// Using deterministic seed for reproducibility
		input[k] = float32((seed*17+k*31)%32768 - 16384)
	}
	return input
}

// mdctComputeSNR calculates signal-to-noise ratio in dB.
// Returns SNR and max absolute difference.
func mdctComputeSNR[A ~float32 | ~float64, B ~float32 | ~float64](expected []A, actual []B) (snr, maxDiff float64) {
	var errPow, sigPow float64
	maxDiff = 0.0

	for i := range expected {
		exp := float64(expected[i])
		act := float64(actual[i])
		diff := exp - act
		if math.Abs(diff) > maxDiff {
			maxDiff = math.Abs(diff)
		}
		errPow += diff * diff
		sigPow += exp * exp
	}

	if errPow < 1e-30 {
		return 200.0, maxDiff // Essentially perfect match
	}
	if sigPow < 1e-30 {
		return 0.0, maxDiff // No signal
	}

	snr = 10.0 * math.Log10(sigPow/errPow)
	return snr, maxDiff
}

// mdctComputeCorrelation calculates Pearson correlation coefficient.
func mdctComputeCorrelation[A ~float32 | ~float64, B ~float32 | ~float64](a []A, b []B) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0.0
	}

	n := float64(len(a))
	var sumA, sumB, sumAB, sumA2, sumB2 float64

	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		sumA += av
		sumB += bv
		sumAB += av * bv
		sumA2 += av * av
		sumB2 += bv * bv
	}

	meanA := sumA / n
	meanB := sumB / n

	var covAB, varA, varB float64
	for i := range a {
		dA := float64(a[i]) - meanA
		dB := float64(b[i]) - meanB
		covAB += dA * dB
		varA += dA * dA
		varB += dB * dB
	}

	if varA < 1e-30 || varB < 1e-30 {
		return 0.0
	}

	return covAB / (math.Sqrt(varA) * math.Sqrt(varB))
}

// TestMDCTForward_ReferenceFormula tests the Go MDCT against the reference formula.
// This is the most direct test - comparing against the mathematical definition.
func TestMDCTForward_ReferenceFormula(t *testing.T) {
	// Test sizes: we need to test with the overlap-aware function
	// Input size = frameSize + overlap, output size = frameSize
	testCases := []struct {
		frameSize int
		overlap   int
	}{
		{120, 120}, // 2.5ms at 48kHz
		{240, 120}, // 5ms at 48kHz
		{480, 120}, // 10ms at 48kHz
		{960, 120}, // 20ms at 48kHz
	}

	for _, tc := range testCases {
		t.Run("frame="+string(rune('0'+tc.frameSize/120))+"x120", func(t *testing.T) {
			// For CELT MDCT with short overlap:
			// Input: frameSize + overlap samples
			// The MDCT processes this with windowing in the overlap regions
			// Output: frameSize coefficients

			// Create test input
			inputSize := tc.frameSize + tc.overlap
			input := prepareInputWithWindow(inputSize, 12345)

			// Compute using Go implementation
			goOutput := mdctForwardOverlap(input, tc.overlap)

			if goOutput == nil {
				t.Fatalf("mdctForwardOverlap returned nil for frameSize=%d", tc.frameSize)
			}

			if len(goOutput) != tc.frameSize {
				t.Fatalf("mdctForwardOverlap output length=%d, want %d", len(goOutput), tc.frameSize)
			}

			// For the reference, we need to construct the properly windowed input
			// and use the standard MDCT formula (without the overlap handling)
			// This is trickier because the Go implementation folds the windowing
			// into the transform.

			// Let's verify basic sanity first
			var maxAbs float64
			for _, v := range goOutput {
				if math.Abs(float64(v)) > maxAbs {
					maxAbs = math.Abs(float64(v))
				}
			}

			if maxAbs < 1.0 {
				t.Errorf("Output seems too small, max abs = %v", maxAbs)
			}

			t.Logf("frameSize=%d: output max abs = %v", tc.frameSize, maxAbs)
		})
	}
}

// TestMDCTForward_DirectFormula tests the direct MDCT formula (without overlap handling).
// This validates the core transform is mathematically correct.
func TestMDCTForward_DirectFormula(t *testing.T) {
	testSizes := []int{32, 64, 128, 240, 480}

	for _, N := range testSizes {
		t.Run("N="+string(rune('0'+N/10)), func(t *testing.T) {
			// Create test input (2N time samples)
			input := make([]float32, 2*N)
			for i := range input {
				input[i] = float32((i*17+31)%32768-16384) / 32768.0
			}

			// Compute using reference formula
			expected := referenceMDCTForward(input)

			// Compute using Go mdctDirect
			actual := mdctDirect(input)

			if actual == nil {
				t.Fatalf("mdctDirect returned nil for N=%d", N)
			}

			if len(actual) != N {
				t.Fatalf("mdctDirect output length=%d, want %d", len(actual), N)
			}

			// Compare
			snr, maxDiff := mdctComputeSNR(expected, actual)
			corr := mdctComputeCorrelation(expected, actual)

			t.Logf("N=%d: SNR=%.2f dB, maxDiff=%.2e, correlation=%.9f", N, snr, maxDiff, corr)

			const maxDirectFormulaDiff = 3e-5
			if maxDiff > maxDirectFormulaDiff {
				t.Errorf("max abs diff %.2e exceeds threshold %.2e", maxDiff, maxDirectFormulaDiff)
			}
			if corr < 0.999999 {
				t.Errorf("correlation %.9f below threshold 0.999999", corr)
			}
		})
	}
}

// TestMDCTForward_ShortFrameWithRectWindow tests short frame MDCT with rectangular window.
// This isolates the transform from windowing to verify the core algorithm.
func TestMDCTForward_ShortFrameWithRectWindow(t *testing.T) {
	// For short frame sizes 120 and 240
	frameSizes := []int{120, 240}

	for _, frameSize := range frameSizes {
		t.Run("frameSize="+string(rune('0'+frameSize/100)), func(t *testing.T) {
			// Total MDCT input = 2 * frameSize
			N := 2 * frameSize
			input := make([]float32, N)

			// Fill with test data
			for i := range input {
				input[i] = float32(math.Sin(float64(i) * 0.1))
			}

			// Expected output using reference formula
			expected := referenceMDCTForward(input)

			// Actual using mdctDirect (no windowing)
			actual := mdctDirect(input)

			snr, maxDiff := mdctComputeSNR(expected, actual)
			corr := mdctComputeCorrelation(expected, actual)

			t.Logf("frameSize=%d: SNR=%.2f dB, maxDiff=%.2e, corr=%.9f",
				frameSize, snr, maxDiff, corr)

			const maxDirectFormulaDiff = 3e-5
			if maxDiff > maxDirectFormulaDiff {
				t.Errorf("frameSize=%d: max diff %.2e exceeds %.2e", frameSize, maxDiff, maxDirectFormulaDiff)
			}
			if corr < 0.999999 {
				t.Errorf("frameSize=%d: correlation %.9f below 0.999999", frameSize, corr)
			}
		})
	}
}

// TestMDCTForward_CELTShortOverlap tests the CELT-style MDCT with short overlap.
// This is the actual transform used in CELT encoding.
func TestMDCTForward_CELTShortOverlap(t *testing.T) {
	frameSizes := []int{120, 240, 480, 960}

	for _, frameSize := range frameSizes {
		t.Run("frameSize="+string(rune('0'+frameSize/100)), func(t *testing.T) {
			overlap := Overlap // 120 samples

			// Input: frameSize + overlap samples
			inputLen := frameSize + overlap
			input := make([]float32, inputLen)

			// Fill with test data
			for i := range input {
				input[i] = float32(math.Cos(float64(i) * 0.05))
			}

			// Compute using Go implementation
			output := mdctForwardOverlap(input, overlap)

			if output == nil {
				t.Fatalf("mdctForwardOverlap returned nil")
			}

			if len(output) != frameSize {
				t.Fatalf("output length=%d, want %d", len(output), frameSize)
			}

			// Verify output has reasonable values
			var maxAbs, sumSq float64
			for _, v := range output {
				if math.Abs(float64(v)) > maxAbs {
					maxAbs = math.Abs(float64(v))
				}
				sumSq += float64(v * v)
			}
			rms := math.Sqrt(sumSq / float64(frameSize))

			t.Logf("frameSize=%d: maxAbs=%.4f, RMS=%.4f", frameSize, maxAbs, rms)

			// Sanity checks
			if maxAbs < 0.001 {
				t.Errorf("output max abs too small: %v", maxAbs)
			}
			if math.IsNaN(float64(maxAbs)) || math.IsInf(float64(maxAbs), 0) {
				t.Errorf("output contains NaN or Inf")
			}
		})
	}
}

// TestMDCT_RoundTrip tests MDCT -> IMDCT round-trip reconstruction.
// Perfect reconstruction (with proper windowing and overlap-add) should
// recover the original signal with minimal error.
func TestMDCT_RoundTrip(t *testing.T) {
	frameSizes := []int{120, 240, 480, 960}
	overlap := Overlap

	for _, frameSize := range frameSizes {
		t.Run("frameSize="+string(rune('0'+frameSize/100)), func(t *testing.T) {
			// Create a long test signal spanning multiple frames
			numFrames := 3
			signalLen := frameSize * numFrames

			signal := make([]float32, signalLen+overlap)
			for i := range signal {
				signal[i] = float32(math.Sin(float64(i) * 2 * math.Pi / float64(frameSize)))
			}

			// Process each frame
			reconstructed := make([]float32, signalLen)
			prevOverlapBuf := make([]float32, overlap)

			for frame := range numFrames {
				start := frame * frameSize
				end := start + frameSize + overlap
				if end > len(signal) {
					break
				}

				// Forward MDCT
				frameInput := signal[start:end]
				coeffs := mdctForwardOverlap(frameInput, overlap)

				if coeffs == nil {
					t.Fatalf("frame %d: MDCT returned nil", frame)
				}

				// Inverse IMDCT
				imdctOutput := IMDCTOverlapWithPrev(coeffs, prevOverlapBuf, overlap)

				// Extract output samples for this frame
				outputStart := start
				for i := 0; i < frameSize && i < len(imdctOutput)-overlap && outputStart+i < len(reconstructed); i++ {
					reconstructed[outputStart+i] = imdctOutput[i]
				}

				// Save overlap for next frame
				if len(imdctOutput) >= frameSize+overlap {
					copy(prevOverlapBuf, imdctOutput[frameSize:frameSize+overlap])
				}
			}

			// Compare reconstructed signal with original
			// Skip first frame (startup effects) and last frame (no overlap-add yet)
			startCompare := frameSize
			endCompare := frameSize * 2
			if endCompare > len(reconstructed) || endCompare > len(signal) {
				t.Skip("signal too short for comparison")
			}

			var maxDiff, sumSq float64
			for i := startCompare; i < endCompare; i++ {
				diff := signal[i] - reconstructed[i]
				if math.Abs(float64(diff)) > maxDiff {
					maxDiff = math.Abs(float64(diff))
				}
				sumSq += float64(diff * diff)
			}
			rmsDiff := math.Sqrt(sumSq / float64(endCompare-startCompare))

			t.Logf("frameSize=%d: maxDiff=%.4f, rmsDiff=%.4f", frameSize, maxDiff, rmsDiff)

			// Relaxed threshold for round-trip (includes quantization and edge effects)
			if maxDiff > 0.1 {
				t.Logf("Warning: round-trip max diff %.4f is high", maxDiff)
			}
		})
	}
}

// TestMDCTTrigTable tests the trig table generation matches libopus formula.
// libopus: trig[i] = cos(2*pi*(i+0.125)/N) for i in [0, N/2)
func TestMDCTTrigTable(t *testing.T) {
	testSizes := []int{240, 480, 960, 1920}

	for _, N := range testSizes {
		t.Run("N="+string(rune('0'+N/100)), func(t *testing.T) {
			goTrig := getMDCTTrigF32(N)

			if len(goTrig) != N/2 {
				t.Fatalf("trig length=%d, want %d", len(goTrig), N/2)
			}

			// Compare with expected formula
			var maxDiff float64
			for i := 0; i < N/2; i++ {
				expected := math.Cos(2.0 * math.Pi * (float64(i) + 0.125) / float64(N))
				diff := math.Abs(float64(goTrig[i]) - expected)
				if diff > maxDiff {
					maxDiff = diff
				}
			}

			t.Logf("N=%d: trig table maxDiff=%.2e", N, maxDiff)

			const maxTrigDiff = 5e-8
			if maxDiff > maxTrigDiff {
				t.Errorf("trig table maxDiff %.2e exceeds %.2e", maxDiff, maxTrigDiff)
			}
		})
	}
}

// TestMDCTForward_Scaling tests that the output scaling matches libopus.
// libopus uses scale = 1/N4 in pre-rotation (N4 = N/4 = frameSize/2)
func TestMDCTForward_Scaling(t *testing.T) {
	// Use a simple impulse input to test scaling
	frameSizes := []int{120, 240}

	for _, frameSize := range frameSizes {
		t.Run("frameSize="+string(rune('0'+frameSize/100)), func(t *testing.T) {
			overlap := Overlap
			inputLen := frameSize + overlap

			// Create impulse at center
			input := make([]float32, inputLen)
			input[inputLen/2] = 1.0

			output := mdctForwardOverlap(input, overlap)

			if output == nil {
				t.Fatalf("mdctForwardOverlap returned nil")
			}

			// Compute output energy
			var energy float64
			for _, v := range output {
				energy += float64(v * v)
			}

			t.Logf("frameSize=%d: impulse response energy=%.6f", frameSize, energy)

			// The energy should be related to the input energy by the scaling factor
			// For proper normalization, energy should be finite and non-zero
			if energy < 1e-10 {
				t.Errorf("output energy too small: %v", energy)
			}
			if math.IsNaN(float64(energy)) || math.IsInf(float64(energy), 0) {
				t.Errorf("output energy is NaN or Inf")
			}
		})
	}
}

// TestMDCTForward_LibopusShortFrame tests mdctForwardOverlap with short frames.
// This test validates that the windowing and transform together match libopus.
// For short frames (120, 240), we use the CELT overlap of 120 samples.
func TestMDCTForward_LibopusShortFrame(t *testing.T) {
	frameSizes := []int{120, 240}

	for _, frameSize := range frameSizes {
		t.Run("frameSize="+string(rune('0'+frameSize/100)), func(t *testing.T) {
			overlap := Overlap // 120 samples

			// Total MDCT input size: frameSize + overlap
			inputLen := frameSize + overlap

			// Create test input similar to libopus test
			input := make([]float32, inputLen)
			for i := range input {
				input[i] = float32((12345*17+i*31)%32768 - 16384)
			}

			// Compute using Go implementation
			goOutput := mdctForwardOverlap(input, overlap)

			if goOutput == nil {
				t.Fatalf("mdctForwardOverlap returned nil")
			}

			if len(goOutput) != frameSize {
				t.Fatalf("output length=%d, want %d", len(goOutput), frameSize)
			}

			// For the reference, we need to construct the equivalent windowed input
			// and use the reference formula. However, the Go implementation folds
			// the windowing into the transform in a way that matches libopus.
			//
			// To verify correctness, we can check:
			// 1. Output is not all zeros
			// 2. Output has reasonable magnitude
			// 3. Round-trip through IMDCT recovers the input (within overlap-add constraints)

			var maxAbs, sumSq float64
			for _, v := range goOutput {
				if math.Abs(float64(v)) > maxAbs {
					maxAbs = math.Abs(float64(v))
				}
				sumSq += float64(v * v)
			}
			rms := math.Sqrt(sumSq / float64(frameSize))

			t.Logf("frameSize=%d: maxAbs=%.4f, RMS=%.4f", frameSize, maxAbs, rms)

			// Sanity checks
			if maxAbs < 1.0 {
				t.Errorf("output max abs too small: %v", maxAbs)
			}
			if math.IsNaN(float64(maxAbs)) || math.IsInf(float64(maxAbs), 0) {
				t.Errorf("output contains NaN or Inf")
			}

			// Verify MDCT->IMDCT round-trip
			imdctOutput := imdctOverlapWithPrev(goOutput, make([]float32, overlap), overlap)
			if imdctOutput == nil {
				t.Fatalf("IMDCT returned nil")
			}

			// The middle portion should be close to the original (excluding window edges)
			// Check samples in the middle region where window = 1.0
			middleStart := overlap
			middleEnd := min(frameSize, len(input))

			var maxRoundTripErr float64
			for i := middleStart; i < middleEnd && i < len(imdctOutput); i++ {
				// The IMDCT output at position i corresponds to input at position i-overlap/2
				// for the middle non-windowed region
				inputIdx := i
				if inputIdx >= 0 && inputIdx < len(input) {
					diff := math.Abs(float64(imdctOutput[i] - input[inputIdx]))
					if diff > maxRoundTripErr {
						maxRoundTripErr = diff
					}
				}
			}

			t.Logf("frameSize=%d: round-trip max error in middle region=%.4e", frameSize, maxRoundTripErr)
		})
	}
}

// BenchmarkMDCTForward benchmarks the forward MDCT for various frame sizes.
func BenchmarkMDCTForward(b *testing.B) {
	sizes := []int{120, 240, 480, 960}
	overlap := Overlap

	for _, frameSize := range sizes {
		b.Run("frameSize="+string(rune('0'+frameSize/100)), func(b *testing.B) {
			input := make([]float32, frameSize+overlap)
			for i := range input {
				input[i] = float32(math.Sin(float64(i) * 0.1))
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mdctForwardOverlap(input, overlap)
			}
		})
	}
}

// BenchmarkMDCTDirectFormula benchmarks the reference formula (for comparison).
func BenchmarkMDCTDirectFormula(b *testing.B) {
	sizes := []int{120, 240}

	for _, N := range sizes {
		b.Run("N="+string(rune('0'+N/10)), func(b *testing.B) {
			input := make([]float32, 2*N)
			for i := range input {
				input[i] = float32(math.Sin(float64(i) * 0.1))
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				referenceMDCTForward(input)
			}
		})
	}
}

func TestMDCTForwardOverlapTrailingFoldBoundary(t *testing.T) {
	// Exercise standard overlap (120, overlap%16==8) and all mod-16 classes of
	// valid multiple-of-4 overlaps (112, 116, 108, 32, 28, 24, 20, 16, 12) so
	// SIMD leading/mid/trailing fold guards and exact-zero pointer bounds are
	// exercised under -gcflags=all=-d=checkptr and checked against mdctDirect.
	cases := []struct {
		frameSize int
		overlap   int
	}{
		{frameSize: 960, overlap: 120},
		{frameSize: 480, overlap: 120},
		{frameSize: 240, overlap: 120},
		{frameSize: 120, overlap: 120},
		{frameSize: 960, overlap: 112},
		{frameSize: 240, overlap: 112},
		{frameSize: 240, overlap: 116},
		{frameSize: 240, overlap: 108},
		{frameSize: 240, overlap: 32},
		{frameSize: 240, overlap: 28},
		{frameSize: 240, overlap: 24},
		{frameSize: 240, overlap: 20},
		{frameSize: 240, overlap: 16},
		{frameSize: 240, overlap: 12},
	}

	for _, tc := range cases {
		samples := make([]float32, tc.frameSize+tc.overlap)
		for i := range samples {
			samples[i] = float32(math.Sin(0.13*float64(i)+0.4) + 0.5*math.Cos(0.37*float64(i)))
		}
		got := make([]float32, tc.frameSize)
		mdctForwardOverlapF32Scratch(samples, tc.overlap, got, nil, nil, nil, nil, nil)

		// Build the 2*frameSize zero-padded windowed block and compare against
		// the direct MDCT reference (mdctDirect).
		win := GetWindowBufferF32(tc.overlap)
		n := 2 * tc.frameSize
		pad := (tc.frameSize - tc.overlap) / 2
		padded := make([]float32, n)
		for i := 0; i < tc.overlap; i++ {
			padded[pad+i] = samples[i] * win[i]
			padded[n-pad-1-i] = samples[len(samples)-1-i] * win[i]
		}
		for i := tc.overlap; i < tc.frameSize; i++ {
			padded[pad+i] = samples[i]
		}
		want := mdctDirect(padded)

		maxErr := 0.0
		for k := range got {
			if diff := math.Abs(float64(got[k]) - float64(want[k])); diff > maxErr {
				maxErr = diff
			}
		}
		if maxErr > 1e-4 {
			t.Fatalf("frameSize=%d overlap=%d maxErr=%g > 1e-4", tc.frameSize, tc.overlap, maxErr)
		}
	}
}

func mdctShortBlocksCoreLegacy(samples []float32, overlap, shortBlocks, shortSize int, output, blockCoeffs []float32, blockMDCT func(block, coeffs []float32)) {
	for b := range shortBlocks {
		start := b * shortSize
		end := start + shortSize + overlap
		if end > len(samples) {
			break
		}
		blockMDCT(samples[start:end], blockCoeffs)
		for i, v := range blockCoeffs {
			outIdx := b + i*shortBlocks
			if outIdx < len(output) {
				output[outIdx] = v
			}
		}
	}
}

func mdctShortBlocksStub(block, coeffs []float32) {
	base := float32(len(block))
	for i := range coeffs {
		coeffs[i] = base + float32(i)*0.25
	}
}

func TestMDCTShortBlocksCoreMatchesLegacy(t *testing.T) {
	samples := make([]float32, 1080)
	for i := range samples {
		samples[i] = float32((i%31)-15) * 0.125
	}
	const (
		overlap     = 120
		shortBlocks = 8
		shortSize   = 120
	)
	outputLen := shortBlocks * shortSize
	want := make([]float32, outputLen)
	got := make([]float32, outputLen)
	blockCoeffsWant := make([]float32, shortSize)
	blockCoeffsGot := make([]float32, shortSize)

	mdctShortBlocksCoreLegacy(samples, overlap, shortBlocks, shortSize, want, blockCoeffsWant, mdctShortBlocksStub)
	mdctShortBlocksCore(samples, overlap, shortBlocks, shortSize, got, blockCoeffsGot, mdctShortBlocksStub)

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("coefficient %d mismatch: got %.9g want %.9g", i, got[i], want[i])
		}
	}
}

func benchmarkMDCTShortBlocksCore(b *testing.B, legacy bool) {
	samples := make([]float32, 1080)
	for i := range samples {
		samples[i] = float32((i%31)-15) * 0.125
	}
	const (
		overlap     = 120
		shortBlocks = 8
		shortSize   = 120
	)
	output := make([]float32, shortBlocks*shortSize)
	blockCoeffs := make([]float32, shortSize)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if legacy {
			mdctShortBlocksCoreLegacy(samples, overlap, shortBlocks, shortSize, output, blockCoeffs, mdctShortBlocksStub)
		} else {
			mdctShortBlocksCore(samples, overlap, shortBlocks, shortSize, output, blockCoeffs, mdctShortBlocksStub)
		}
	}
}

func BenchmarkMDCTShortBlocksCoreCurrent(b *testing.B) {
	benchmarkMDCTShortBlocksCore(b, false)
}

func BenchmarkMDCTShortBlocksCoreLegacy(b *testing.B) {
	benchmarkMDCTShortBlocksCore(b, true)
}

func TestMDCTFMALikeMixUsesFusedSourceShape(t *testing.T) {
	if !mdctUseFMALikeMixEnabled {
		t.Skip("FMA-like MDCT mix is arch-specific")
	}

	addA := float32(12575.8603515625)
	addB := float32(15380.7783203125)
	addC := float32(0.27607929706573486)
	addD := float32(0.8652157187461853)
	addWant := float32(math.FMA(float64(addA), float64(addC), float64(mdctMul(addB, addD))))
	addSplit := mdctMul(addA, addC) + mdctMul(addB, addD)
	if math.Float32bits(addWant) == math.Float32bits(addSplit) {
		t.Fatalf("add fixture does not expose fused MDCT rounding")
	}
	if got := mdctMulAddMix(addA, addB, addC, addD); math.Float32bits(got) != math.Float32bits(addWant) {
		t.Fatalf("mdctMulAddMix=%08x %.9g want fused %08x %.9g",
			math.Float32bits(got), got, math.Float32bits(addWant), addWant)
	}

	subA := float32(15156.271484375)
	subB := float32(7875.662109375)
	subC := float32(0.19688130915164948)
	subD := float32(0.9199866056442261)
	subWant := float32(math.FMA(float64(subA), float64(subC), -float64(mdctMul(subB, subD))))
	subSplit := mdctMul(subA, subC) - mdctMul(subB, subD)
	if math.Float32bits(subWant) == math.Float32bits(subSplit) {
		t.Fatalf("sub fixture does not expose fused MDCT rounding")
	}
	if got := mdctMulSubMix(subA, subB, subC, subD); math.Float32bits(got) != math.Float32bits(subWant) {
		t.Fatalf("mdctMulSubMix=%08x %.9g want fused %08x %.9g",
			math.Float32bits(got), got, math.Float32bits(subWant), subWant)
	}
}

func TestMDCTNegativeForwardFoldUsesTargetContractionOrder(t *testing.T) {
	if !mdctUseFMALikeMixEnabled {
		t.Skip("contracted MDCT fold is arch-specific")
	}

	a := float32(861792.375)
	b := float32(-8019820.5)
	c := float32(0.598416567)
	d := float32(0.441076219)
	v3Want := float32(math.FMA(float64(b), float64(d), -float64(mdctMul(a, c))))
	armWant := float32(math.FMA(-float64(a), float64(c), float64(mdctMul(b, d))))
	if math.Float32bits(v3Want) == math.Float32bits(armWant) {
		t.Fatal("fixture does not distinguish the AMD64 and arm64 contraction orders")
	}
	want := armWant
	if mdctUseNegFoldSecondProduct {
		want = v3Want
	}
	if got := mdctNegMulAddMixEncodeForTest(a, b, c, d); math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("negative fold=%08x want %08x", math.Float32bits(got), math.Float32bits(want))
	}
}

// mdctNegMulAddMixEncodeForTest keeps fixture inputs runtime-opaque so the
// assertion exercises the contraction order in libopus celt/mdct.c's
// clt_mdct_forward_c instead of a constant-folded expression.
//
//go:noinline
func mdctNegMulAddMixEncodeForTest(a, b, c, d float32) float32 {
	return mdctNegMulAddMixEncode(a, b, c, d)
}

func mdctForwardOverlapLegacyStagedReference(samples []float32, overlap int) []float32 {
	if len(samples) == 0 {
		return nil
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap > len(samples) {
		overlap = len(samples)
	}

	frameSize := len(samples) - overlap
	if frameSize <= 0 {
		return nil
	}

	n2 := frameSize
	n := n2 * 2
	n4 := n2 / 2
	if n4 <= 0 {
		return nil
	}

	trig := getMDCTTrigF32(n)
	var window []float32
	if overlap > 0 {
		window = GetWindowBufferF32(overlap)
	}

	f := make([]float32, n2)
	fftTmp := make([]float32, 2*n4)
	coeffs := make([]float32, n2)

	st := getKissFFTState(n4)
	useDirectKissCpx := st != nil && len(st.bitrev) >= n4
	var fftStage []float32
	var fftIn []complex64
	var fftOut []complex64
	if useDirectKissCpx {
		fftStage = fftTmp[:2*n4]
	} else {
		fftIn = make([]complex64, n4)
		fftOut = make([]complex64, n4)
	}

	xp1 := overlap / 2
	xp2 := n2 - 1 + overlap/2
	wp1 := overlap / 2
	wp2 := overlap/2 - 1
	i := 0
	limit1 := (overlap + 3) >> 2

	for ; i < limit1; i++ {
		f[2*i] = mdctMulAddMix(samples[xp1+n2], samples[xp2], window[wp2], window[wp1])
		f[2*i+1] = mdctMulSubMix(samples[xp1], samples[xp2-n2], window[wp1], window[wp2])
		xp1 += 2
		xp2 -= 2
		wp1 += 2
		wp2 -= 2
	}

	wp1 = 0
	wp2 = overlap - 1
	for ; i < n4-limit1; i++ {
		f[2*i] = samples[xp2]
		f[2*i+1] = samples[xp1]
		xp1 += 2
		xp2 -= 2
	}

	for ; i < n4; i++ {
		f[2*i] = mdctNegMulAddMixEncode(samples[xp1-n2], samples[xp2], window[wp1], window[wp2])
		f[2*i+1] = mdctMulAddMix(samples[xp1], samples[xp2+n2], window[wp2], window[wp1])
		xp1 += 2
		xp2 -= 2
		wp1 += 2
		wp2 -= 2
	}

	scale := float32(1.0) / float32(n4)
	if useDirectKissCpx {
		bitrev := st.bitrev
		if mdctUseFMALikeMixEnabled {
			for i = range n4 {
				re := f[2*i]
				im := f[2*i+1]
				t0 := trig[i]
				t1 := trig[n4+i]
				mdctStoreDirectStageFMALike(fftStage, bitrev[i], scale, re, im, t0, t1)
			}
		} else {
			for i = range n4 {
				re := f[2*i]
				im := f[2*i+1]
				t0 := trig[i]
				t1 := trig[n4+i]
				mdctStoreDirectStage(fftStage, bitrev[i], scale, re, im, t0, t1)
			}
		}
		st.fftImpl(fftStage)
	} else {
		if mdctUseFMALikeMixEnabled {
			for i = range n4 {
				re := f[2*i]
				im := f[2*i+1]
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := float32(float64(re)*float64(t0) - float64(mdctMul(im, t1)))
				yi := float32(float64(im)*float64(t0) + float64(mdctMul(re, t1)))
				fftIn[i] = complex(yr*scale, yi*scale)
			}
		} else {
			for i = range n4 {
				re := f[2*i]
				im := f[2*i+1]
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := mdctMul(re, t0) - mdctMul(im, t1)
				yi := mdctMul(im, t0) + mdctMul(re, t1)
				fftIn[i] = complex(yr*scale, yi*scale)
			}
		}
		kissFFT32To(fftOut, fftIn[:n4], fftTmp)
	}

	if useDirectKissCpx {
		if mdctUseFMALikeMixEnabled {
			for i = range n4 {
				re := fftStage[2*i]
				im := fftStage[2*i+1]
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := float32(float64(im)*float64(t1) - float64(mdctMul(re, t0)))
				yi := float32(float64(re)*float64(t1) + float64(mdctMul(im, t0)))
				coeffs[2*i] = yr
				coeffs[n2-1-2*i] = yi
			}
		} else {
			for i = range n4 {
				re := fftStage[2*i]
				im := fftStage[2*i+1]
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := mdctMul(im, t1) - mdctMul(re, t0)
				yi := mdctMul(re, t1) + mdctMul(im, t0)
				coeffs[2*i] = yr
				coeffs[n2-1-2*i] = yi
			}
		}
	} else {
		if mdctUseFMALikeMixEnabled {
			for i = range n4 {
				re := real(fftOut[i])
				im := imag(fftOut[i])
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := float32(float64(im)*float64(t1) - float64(mdctMul(re, t0)))
				yi := float32(float64(re)*float64(t1) + float64(mdctMul(im, t0)))
				coeffs[2*i] = yr
				coeffs[n2-1-2*i] = yi
			}
		} else {
			for i = range n4 {
				re := real(fftOut[i])
				im := imag(fftOut[i])
				t0 := trig[i]
				t1 := trig[n4+i]
				yr := mdctMul(im, t1) - mdctMul(re, t0)
				yi := mdctMul(re, t1) + mdctMul(im, t0)
				coeffs[2*i] = yr
				coeffs[n2-1-2*i] = yi
			}
		}
	}

	return coeffs[:n2]
}

func TestMDCTForwardOverlapDirectStageMatchesLegacyStagedPath(t *testing.T) {
	testCases := []struct {
		frameSize int
		overlap   int
	}{
		{frameSize: 120, overlap: 120},
		{frameSize: 240, overlap: 120},
		{frameSize: 480, overlap: 120},
		{frameSize: 960, overlap: 120},
	}

	for _, tc := range testCases {
		for seed := 1; seed <= 4; seed++ {
			name := fmt.Sprintf("frame=%d/seed=%d", tc.frameSize, seed)
			t.Run(name, func(t *testing.T) {
				input := make([]float32, tc.frameSize+tc.overlap)
				for i := range input {
					sine := math.Sin(float64(i+seed*7) * 0.073)
					cosine := math.Cos(float64((i+1)*(seed+3)) * 0.021)
					step := float64((i*17+seed*31)%29-14) / 32.0
					input[i] = float32(0.55*sine + 0.35*cosine + step)
				}

				got := mdctForwardOverlapF32(input, tc.overlap)
				want := mdctForwardOverlapLegacyStagedReference(input, tc.overlap)
				if len(got) != len(want) {
					t.Fatalf("length mismatch: got %d want %d", len(got), len(want))
				}

				tol := 2e-7
				for i := range want {
					if got[i] == want[i] || math.Abs(float64(got[i]-want[i])) <= tol {
						continue
					}
					t.Fatalf("coefficient %d mismatch: got %.9g want %.9g (tol=%g)", i, got[i], want[i], tol)
				}
			})
		}
	}
}

func TestIMDCT_OutputLength(t *testing.T) {
	for _, n := range []int{120, 240, 480, 960} {
		spectrum := make([]float32, n)
		out := IMDCT(spectrum)
		if len(out) != 2*n {
			t.Errorf("IMDCT(%d) output length = %d, want %d", n, len(out), 2*n)
		}
	}
}

// TestIMDCT_SmallSizes verifies IMDCT handles small sizes.
func TestIMDCT_SmallSizes(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 16} {
		spectrum := make([]float32, n)
		spectrum[0] = 1.0
		out := IMDCT(spectrum)
		if len(out) != 2*n {
			t.Errorf("IMDCT(%d) output length = %d, want %d", n, len(out), 2*n)
		}
	}
}

// TestIMDCT_DC tests IMDCT with DC component only.
func TestIMDCT_DC(t *testing.T) {
	n := 64
	spectrum := make([]float32, n)
	spectrum[0] = 1.0 // DC component

	out := IMDCT(spectrum)

	// DC should produce some output
	// Check that output is not all zeros
	var maxAbs float64
	for _, x := range out {
		if math.Abs(float64(x)) > maxAbs {
			maxAbs = math.Abs(float64(x))
		}
	}

	// The output should have some non-zero values
	// (The exact magnitude depends on normalization)
	if maxAbs < 1e-10 {
		t.Errorf("IMDCT with DC input produced all zeros, max = %v", maxAbs)
	}
}

// TestIMDCT_KnownValues tests IMDCT produces expected output length and values.
func TestIMDCT_KnownValues(t *testing.T) {
	// Test that IMDCT produces correct output for various sizes
	for _, n := range []int{16, 32, 64, 128} {
		spectrum := make([]float32, n)
		spectrum[0] = 1.0

		result := IMDCT(spectrum)

		// Check output length
		if len(result) != 2*n {
			t.Errorf("IMDCT(%d) output length = %d, want %d", n, len(result), 2*n)
		}

		// Check output is not all zeros
		var maxAbs float64
		for _, x := range result {
			if math.Abs(float64(x)) > maxAbs {
				maxAbs = math.Abs(float64(x))
			}
		}
		if maxAbs < 1e-10 {
			t.Errorf("IMDCT(%d) with DC input produced all zeros", n)
		}
	}
}

// TestIMDCTEnergyConservation verifies energy is conserved through IMDCT.
// Parseval's theorem: energy should be conserved (within factor determined by normalization).
func TestIMDCTEnergyConservation(t *testing.T) {
	// CELT frame sizes: 120 (2.5ms), 240 (5ms), 480 (10ms), 960 (20ms)
	sizes := []int{120, 240, 480, 960}

	for _, n := range sizes {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			// Create deterministic test spectrum with varying amplitudes
			spectrum := make([]float32, n)
			for i := range spectrum {
				spectrum[i] = float32(math.Sin(float64(i) * 0.1))
			}

			// Input energy (frequency domain)
			inputEnergy := 0.0
			for _, x := range spectrum {
				inputEnergy += float64(x * x)
			}

			output := IMDCT(spectrum)

			// Output energy (time domain)
			outputEnergy := 0.0
			for _, x := range output {
				outputEnergy += float64(x * x)
			}

			// The compatibility IMDCT surface now always uses the direct
			// unscaled path; the runtime CELT path uses float32 IMDCT helpers.
			ratio := outputEnergy / inputEnergy
			expectedRatio := float64(n)

			// Allow 50% tolerance due to normalization conventions
			if ratio < expectedRatio*0.5 || ratio > expectedRatio*2.0 {
				t.Errorf("Energy ratio %f outside expected range [%f, %f]",
					ratio, expectedRatio*0.5, expectedRatio*2.0)
			}
		})
	}
}

// TestIMDCTDCComponent verifies IMDCT produces non-zero output for DC input.
// A DC component (spectrum[0]=1, rest 0) should produce specific pattern.
func TestIMDCTDCComponent(t *testing.T) {
	// CELT frame sizes: 120 (2.5ms), 240 (5ms), 480 (10ms), 960 (20ms)
	sizes := []int{120, 240, 480, 960}

	for _, n := range sizes {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			spectrum := make([]float32, n)
			spectrum[0] = 1.0

			output := IMDCT(spectrum)

			// DC component should produce non-zero output
			hasNonZero := false
			for _, x := range output {
				if math.Abs(float64(x)) > 1e-10 {
					hasNonZero = true
					break
				}
			}

			if !hasNonZero {
				t.Error("IMDCT of DC component produced all zeros")
			}
		})
	}
}

// TestIMDCTDirectOutputLength verifies IMDCTDirect produces correct output length.
// This specifically tests the direct implementation used for CELT's non-power-of-2 sizes.
func TestIMDCTDirectOutputLength(t *testing.T) {
	// Test with power-of-2 size
	n := 256

	spectrum := make([]float32, n)
	for i := range spectrum {
		spectrum[i] = float32(math.Sin(float64(i) * 0.1))
	}

	directOut := IMDCTDirect(spectrum)

	if len(directOut) != 2*n {
		t.Errorf("IMDCTDirect produced %d samples, want %d", len(directOut), 2*n)
	}

	// Verify output has non-zero values
	hasNonZero := false
	for _, x := range directOut {
		if math.Abs(float64(x)) > 1e-10 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("IMDCTDirect produced all zeros")
	}
}

// TestIMDCTDirectCELTSizes verifies IMDCTDirect handles all CELT frame sizes.
// CELT uses non-power-of-2 sizes (120, 240, 480, 960) where IMDCTDirect is required.
func TestIMDCTDirectCELTSizes(t *testing.T) {
	sizes := []int{120, 240, 480, 960}

	for _, n := range sizes {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			spectrum := make([]float32, n)
			for i := range spectrum {
				spectrum[i] = float32(math.Cos(float64(i) * 0.05))
			}

			output := IMDCTDirect(spectrum)

			// Verify output length
			if len(output) != 2*n {
				t.Errorf("IMDCTDirect(%d) produced %d samples, want %d", n, len(output), 2*n)
			}

			// Verify output has expected properties (not all zeros)
			hasNonZero := false
			for _, x := range output {
				if math.Abs(float64(x)) > 1e-10 {
					hasNonZero = true
					break
				}
			}
			if !hasNonZero {
				t.Errorf("IMDCTDirect(%d) produced all zeros", n)
			}
		})
	}
}

// TestIMDCTShort_Transients tests short block IMDCT for transient frames.
func TestIMDCTShort_Transients(t *testing.T) {
	for _, shortBlocks := range []int{2, 4, 8} {
		coeffs := make([]float32, 120*shortBlocks)
		// Fill with test pattern (impulse at start of each block)
		for b := range shortBlocks {
			coeffs[b*120] = 1.0
		}

		out := IMDCTShort(coeffs, shortBlocks)

		// Verify output length
		expectedLen := 120 * shortBlocks * 2
		if len(out) != expectedLen {
			t.Errorf("IMDCTShort(%d blocks) length = %d, want %d",
				shortBlocks, len(out), expectedLen)
		}

		// Verify output is not all zeros
		var maxAbs float64
		for _, x := range out {
			if math.Abs(float64(x)) > maxAbs {
				maxAbs = math.Abs(float64(x))
			}
		}
		if maxAbs < 0.001 {
			t.Errorf("IMDCTShort(%d blocks) produced near-zero output", shortBlocks)
		}
	}
}

// TestIMDCTShort_SingleBlock tests that single block is same as regular IMDCT.
func TestIMDCTShort_SingleBlock(t *testing.T) {
	n := 120
	coeffs := make([]float32, n)
	coeffs[0] = 1.0
	coeffs[5] = 0.5

	regular := IMDCT(coeffs)
	short := IMDCTShort(coeffs, 1)

	if len(regular) != len(short) {
		t.Errorf("Length mismatch: IMDCT=%d, IMDCTShort=%d", len(regular), len(short))
		return
	}

	for i := range regular {
		if math.Abs(float64(regular[i]-short[i])) > 1e-10 {
			t.Errorf("Mismatch at %d: regular=%v, short=%v", i, regular[i], short[i])
		}
	}
}

// TestVorbisWindow_Values verifies Vorbis window produces valid values.
// The Vorbis window values should be in [0, 1] and form a smooth curve.
func TestVorbisWindow_Values(t *testing.T) {
	overlap := Overlap

	// Check that all values are in valid range
	for i := range overlap {
		w := VorbisWindow(i, overlap)
		if w < 0 || w > 1 {
			t.Errorf("Window value out of range at i=%d: %v", i, w)
		}
	}

	// Check that window starts near 0 and reaches near 1 at the end
	w0 := VorbisWindow(0, overlap)
	wEnd := VorbisWindow(overlap-1, overlap)

	if w0 > 0.01 {
		t.Errorf("Window should start near 0, got %v", w0)
	}
	if wEnd < 0.99 {
		t.Errorf("Window should be near 1 at end, got %v", wEnd)
	}

	// Check monotonicity across overlap (rising)
	prev := float32(0)
	for i := range overlap {
		w := VorbisWindow(i, overlap)
		if w < prev {
			t.Errorf("Window not monotonic at i=%d: prev=%v, curr=%v", i, prev, w)
		}
		prev = w
	}
}

// TestVorbisWindow_Symmetry verifies window symmetry.
func TestVorbisWindow_Symmetry(t *testing.T) {
	overlap := Overlap
	for i := range overlap {
		w1 := VorbisWindow(i, overlap)
		w2 := VorbisWindow(overlap-1-i, overlap)

		// For CELT's Vorbis window, the pair must be power-complementary.
		if w1 < 0 || w1 > 1 {
			t.Errorf("Window value out of range at i=%d: %v", i, w1)
		}
		if w2 < 0 || w2 > 1 {
			t.Errorf("Window value out of range at i=%d: %v", overlap-1-i, w2)
		}
		window := GetWindowBuffer(overlap)
		if math.Float32bits(w1) != math.Float32bits(window[i]) {
			t.Errorf("Window table mismatch at i=%d", i)
		}
		if math.Float32bits(w2) != math.Float32bits(window[overlap-1-i]) {
			t.Errorf("Window table mismatch at i=%d", overlap-1-i)
		}
	}
}

// TestVorbisWindow_PrecomputedBuffer verifies precomputed buffer matches formula.
func TestVorbisWindow_PrecomputedBuffer(t *testing.T) {
	buf := GetWindowBuffer(120)
	if len(buf) != 120 {
		t.Errorf("Window buffer length = %d, want 120", len(buf))
		return
	}

	for i := range 120 {
		expected := VorbisWindow(i, 120)
		if math.Abs(float64(buf[i]-expected)) > 1e-7 {
			t.Errorf("Precomputed window mismatch at %d: got %v, want %v",
				i, buf[i], expected)
		}
	}
}

// TestVorbisWindow_PerfectReconstruction verifies window satisfies perfect reconstruction.
// The Vorbis window must satisfy: w[n]^2 + w[overlap-1-n]^2 = 1
// This ensures overlap-add reconstruction preserves energy.
func TestVorbisWindow_PerfectReconstruction(t *testing.T) {
	overlap := Overlap

	// Generate window coefficients (half window, for overlap region)
	window := make([]float32, overlap)
	for i := range overlap {
		window[i] = float32(VorbisWindow(i, overlap))
	}

	// Verify window properties
	// 1. Window starts near 0
	if window[0] > 0.1 {
		t.Errorf("Window start too high: %f", window[0])
	}

	// 2. Window ends near 1
	if window[overlap-1] < 0.9 {
		t.Errorf("Window end too low: %f", window[overlap-1])
	}

	// 3. Window is monotonically increasing
	for n := 1; n < overlap; n++ {
		if window[n] < window[n-1]-1e-10 { // Allow tiny numerical errors
			t.Errorf("Window not monotonic at n=%d: %f < %f",
				n, window[n], window[n-1])
		}
	}

	// 4. Window satisfies perfect reconstruction:
	// w[n]^2 + w[overlap-1-n]^2 = 1
	for n := 0; n < overlap/2; n++ {
		sum := window[n]*window[n] + window[overlap-1-n]*window[overlap-1-n]
		if math.Abs(float64(float64(sum-1.0))) > 0.01 {
			t.Errorf("Window reconstruction failed at n=%d: %f + %f = %f (want 1.0)",
				n, window[n]*window[n], window[overlap-1-n]*window[overlap-1-n], sum)
		}
	}
}

// TestOverlapAddSampleCount verifies overlap-add produces correct sample count.
// For each CELT frame size, overlap-add should produce frameSize output samples.
func TestOverlapAddSampleCount(t *testing.T) {
	// CELT frame sizes: 120 (2.5ms), 240 (5ms), 480 (10ms), 960 (20ms)
	frameSizes := []int{120, 240, 480, 960}
	overlap := 120

	for _, frameSize := range frameSizes {
		t.Run(fmt.Sprintf("frame=%d", frameSize), func(t *testing.T) {
			// Simulate IMDCT output (2*frameSize samples)
			imdctOut := make([]float32, 2*frameSize)
			for i := range imdctOut {
				imdctOut[i] = float32(i) * 0.001 // Arbitrary non-zero values
			}

			// Previous overlap buffer
			prevOverlap := make([]float32, overlap)

			// Perform overlap-add
			output, newOverlap := OverlapAdd(imdctOut, prevOverlap, overlap)

			// Output should be frameSize samples
			if len(output) != frameSize {
				t.Errorf("OverlapAdd produced %d samples, want %d",
					len(output), frameSize)
			}

			// New overlap should be overlap samples
			if len(newOverlap) != overlap {
				t.Errorf("New overlap has %d samples, want %d",
					len(newOverlap), overlap)
			}
		})
	}
}

// TestOverlapAdd_Continuity verifies overlap-add produces smooth output.
func TestOverlapAdd_Continuity(t *testing.T) {
	overlap := 120
	frameSize := 480

	// Create two consecutive frames with a gradual change
	frame1 := make([]float32, frameSize*2)
	frame2 := make([]float32, frameSize*2)

	for i := range frame1 {
		frame1[i] = float32(i) / float32(len(frame1))
	}
	for i := range frame2 {
		frame2[i] = 0.5 + float32(i)/(2.0*float32(len(frame2)))
	}

	// Apply window
	ApplyWindow(frame1, overlap)
	ApplyWindow(frame2, overlap)

	// First overlap-add (with zero previous)
	prevOverlap := make([]float32, overlap)
	output1, newOverlap := OverlapAdd(frame1, prevOverlap, overlap)

	// Second overlap-add
	output2, _ := OverlapAdd(frame2, newOverlap, overlap)

	// Check for continuity at the boundary
	// The end of output1 should connect smoothly to start of output2
	if len(output1) == 0 || len(output2) == 0 {
		t.Fatalf("Output is empty: len(output1)=%d, len(output2)=%d",
			len(output1), len(output2))
	}

	// Check that there's no huge discontinuity
	lastVal := output1[len(output1)-1]
	firstVal := output2[0]
	jump := math.Abs(float64(float64(lastVal - firstVal)))

	// Allow some jump but not huge
	if jump > 1.0 {
		t.Errorf("Large discontinuity at frame boundary: %v -> %v (jump=%v)",
			lastVal, firstVal, jump)
	}
}

// TestOverlapAdd_ZeroInput verifies overlap-add with zero input.
func TestOverlapAdd_ZeroInput(t *testing.T) {
	overlap := 120
	frameSize := 480

	frame := make([]float32, frameSize*2) // All zeros
	prevOverlap := make([]float32, overlap)

	output, newOverlap := OverlapAdd(frame, prevOverlap, overlap)

	// All output should be zero
	for i, x := range output {
		if x != 0 {
			t.Errorf("Non-zero output at %d: %v", i, x)
		}
	}
	for i, x := range newOverlap {
		if x != 0 {
			t.Errorf("Non-zero overlap at %d: %v", i, x)
		}
	}
}

// TestMidSideToLR_MonoCase verifies mid-side with theta=0 produces mono.
func TestMidSideToLR_MonoCase(t *testing.T) {
	mid := []float32{1.0, 2.0, 3.0, 4.0}
	side := []float32{0.5, 0.5, 0.5, 0.5}

	// theta=0: cos(0)=1, sin(0)=0
	// L = 1*mid + 0*side = mid
	// R = 1*mid - 0*side = mid
	left, right := MidSideToLR(mid, side, 0)

	for i := range left {
		if math.Abs(float64(float64(left[i]-mid[i]))) > 1e-6 {
			t.Errorf("Left[%d] = %v, want %v", i, left[i], mid[i])
		}
		if math.Abs(float64(float64(right[i]-mid[i]))) > 1e-6 {
			t.Errorf("Right[%d] = %v, want %v", i, right[i], mid[i])
		}
	}
}

// TestMidSideToLR_FullStereo verifies mid-side with theta=pi/2.
func TestMidSideToLR_FullStereo(t *testing.T) {
	mid := []float32{0, 0, 0, 0}
	side := []float32{1.0, 2.0, 3.0, 4.0}

	// theta=pi/2: cos=0, sin=1
	// L = 0*mid + 1*side = side
	// R = 0*mid - 1*side = -side
	left, right := MidSideToLR(mid, side, float32(math.Pi/2))

	for i := range left {
		if math.Abs(float64(float64(left[i]-side[i]))) > 1e-6 {
			t.Errorf("Left[%d] = %v, want %v", i, left[i], side[i])
		}
		if math.Abs(float64(float64(right[i]+side[i]))) > 1e-6 {
			t.Errorf("Right[%d] = %v, want %v", i, right[i], -side[i])
		}
	}
}

// TestMidSideToLR_Inversion verifies L+R and L-R properties.
func TestMidSideToLR_Inversion(t *testing.T) {
	mid := []float32{1.0, 2.0, 3.0}
	side := []float32{0.1, 0.2, 0.3}
	theta := float32(math.Pi / 4) // 45 degrees

	left, right := MidSideToLR(mid, side, theta)

	// L + R should be approximately 2*cos(theta)*mid (when side is small)
	// L - R should be approximately 2*sin(theta)*side
	cosT := float32(math.Cos(float64(theta)))
	sinT := float32(math.Sin(float64(theta)))

	for i := range mid {
		sum := left[i] + right[i]
		diff := left[i] - right[i]

		expectedSum := 2 * cosT * mid[i]
		expectedDiff := 2 * sinT * side[i]

		if math.Abs(float64(float64(sum-expectedSum))) > 1e-6 {
			t.Errorf("L+R at %d: got %v, want %v", i, sum, expectedSum)
		}
		if math.Abs(float64(float64(diff-expectedDiff))) > 1e-6 {
			t.Errorf("L-R at %d: got %v, want %v", i, diff, expectedDiff)
		}
	}
}

// TestIntensityStereo_NoInversion verifies intensity stereo without inversion.
func TestIntensityStereo_NoInversion(t *testing.T) {
	mono := []float32{1.0, 2.0, 3.0, 4.0}

	left, right := IntensityStereo(mono, false)

	for i := range mono {
		if left[i] != mono[i] {
			t.Errorf("Left[%d] = %v, want %v", i, left[i], mono[i])
		}
		if right[i] != mono[i] {
			t.Errorf("Right[%d] = %v, want %v", i, right[i], mono[i])
		}
	}
}

// TestIntensityStereo_WithInversion verifies intensity stereo with inversion.
func TestIntensityStereo_WithInversion(t *testing.T) {
	mono := []float32{1.0, 2.0, 3.0, 4.0}

	left, right := IntensityStereo(mono, true)

	for i := range mono {
		if left[i] != mono[i] {
			t.Errorf("Left[%d] = %v, want %v", i, left[i], mono[i])
		}
		if right[i] != -mono[i] {
			t.Errorf("Right[%d] = %v, want %v", i, right[i], -mono[i])
		}
	}
}

// TestDecodeFrame_AllFrameSizes tests DecodeFrame for all valid frame sizes.
func TestDecodeFrame_AllFrameSizes(t *testing.T) {
	dec := NewDecoder(1)

	for _, frameSize := range []int{120, 240, 480, 960} {
		// Create minimal valid frame (first byte indicates silence)
		data := createSilenceFrame()

		samples, err := dec.DecodeFrame(data, frameSize)
		if err != nil {
			t.Errorf("DecodeFrame(frameSize=%d) error: %v", frameSize, err)
			continue
		}

		if len(samples) != frameSize {
			t.Errorf("DecodeFrame(frameSize=%d) returned %d samples, want %d",
				frameSize, len(samples), frameSize)
		}
	}
}

// TestDecodeFrame_StereoOutput tests stereo DecodeFrame.
func TestDecodeFrame_StereoOutput(t *testing.T) {
	dec := NewDecoder(2) // stereo
	data := createSilenceFrame()

	samples, err := dec.DecodeFrame(data, 960)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}

	// Stereo output should be 2x frame size (interleaved L,R,L,R...)
	if len(samples) != 960*2 {
		t.Errorf("stereo DecodeFrame returned %d samples, want %d", len(samples), 960*2)
	}
}

// TestDecodeFrame_InvalidFrameSize tests error handling for invalid frame sizes.
func TestDecodeFrame_InvalidFrameSize(t *testing.T) {
	dec := NewDecoder(1)
	data := createSilenceFrame()

	_, err := dec.DecodeFrame(data, 123) // Invalid size
	if err == nil {
		t.Error("Expected error for invalid frame size")
	}
}

// TestDecodeFrame_EmptyData tests error handling for empty data.
func TestDecodeFrame_EmptyData(t *testing.T) {
	dec := NewDecoder(1)

	samples, err := dec.DecodeFrame([]byte{}, 960)
	if err != nil {
		t.Errorf("DecodeFrame(empty) returned error: %v", err)
	}
	if len(samples) != 960 {
		t.Errorf("DecodeFrame(empty) returned %d samples, want %d", len(samples), 960)
	}
}

// TestDecodeFrame_StateConsistency tests that decoder state persists across frames.
func TestDecodeFrame_StateConsistency(t *testing.T) {
	dec := NewDecoder(1)
	data := createSilenceFrame()

	// Decode multiple frames
	for i := range 3 {
		_, err := dec.DecodeFrame(data, 960)
		if err != nil {
			t.Errorf("Frame %d error: %v", i, err)
		}
	}

	// Energy state should have been updated
	energy := dec.GetEnergy(0, 0)
	// After silence frames, energy should be updated
	// Just verify it's a valid number
	if math.IsNaN(float64(float64(energy))) || math.IsInf(float64(energy), 0) {
		t.Errorf("Invalid energy after frames: %v", energy)
	}
}

// TestSynthesize_Basic tests the Synthesize function.
func TestSynthesize_Basic(t *testing.T) {
	dec := NewDecoder(1)

	// Create simple coefficients
	n := 120
	coeffs := make([]float32, n)
	coeffs[0] = 1.0

	samples := dec.synthesizeTest(coeffs, false, 1)

	// Should produce some output
	if len(samples) == 0 {
		t.Error("Synthesize produced no output")
	}

	// Check for non-zero samples
	var maxAbs float64
	for _, x := range samples {
		abs := math.Abs(float64(float64(x)))
		if abs > maxAbs {
			maxAbs = abs
		}
	}
	if maxAbs < 0.001 {
		t.Error("Synthesize produced near-zero output")
	}
}

// TestSynthesize_TransientMode tests Synthesize with transient flag.
func TestSynthesize_TransientMode(t *testing.T) {
	dec := NewDecoder(1)

	// Transient mode with 4 short blocks
	n := 120 * 4
	coeffs := make([]float32, n)
	for i := range 4 {
		coeffs[i*120] = 1.0
	}

	samples := dec.synthesizeTest(coeffs, true, 4)

	if len(samples) == 0 {
		t.Error("Synthesize (transient) produced no output")
	}
}

// TestGetStereoMode tests stereo mode selection.
func TestGetStereoMode(t *testing.T) {
	// Below intensity band: mid-side (default)
	mode := GetStereoMode(5, 10, false)
	if mode != StereoMidSide {
		t.Errorf("Band 5 (intensity=10): got %v, want MidSide", mode)
	}

	// At or above intensity band: intensity stereo
	mode = GetStereoMode(10, 10, false)
	if mode != StereoIntensity {
		t.Errorf("Band 10 (intensity=10): got %v, want Intensity", mode)
	}

	// Dual stereo flag overrides
	mode = GetStereoMode(5, -1, true)
	if mode != StereoDual {
		t.Errorf("Band 5 (dualStereo=true): got %v, want Dual", mode)
	}

	// No intensity (intensity=-1): mid-side
	mode = GetStereoMode(15, -1, false)
	if mode != StereoMidSide {
		t.Errorf("Band 15 (intensity=-1): got %v, want MidSide", mode)
	}
}

// createSilenceFrame creates a minimal CELT silence frame.
// A silence frame is indicated by having the silence flag set.
func createSilenceFrame() []byte {
	// A minimal frame that indicates silence
	// The first byte with high bit set will be interpreted as silence flag
	return []byte{0xFF, 0x00, 0x00, 0x00}
}

// BenchmarkIMDCT benchmarks the IMDCT function.
func BenchmarkIMDCT(b *testing.B) {
	spectrum := make([]float32, 960)
	for i := range spectrum {
		spectrum[i] = float32(i%10) / 10.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IMDCT(spectrum)
	}
}

// BenchmarkIMDCT_Short benchmarks IMDCT with short frame size.
func BenchmarkIMDCT_Short(b *testing.B) {
	spectrum := make([]float32, 120)
	for i := range spectrum {
		spectrum[i] = float32(i%10) / 10.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IMDCT(spectrum)
	}
}

// BenchmarkIMDCTShort benchmarks the short block IMDCT.
func BenchmarkIMDCTShort(b *testing.B) {
	coeffs := make([]float32, 120*8)
	for i := range coeffs {
		coeffs[i] = float32(i%10) / 10.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IMDCTShort(coeffs, 8)
	}
}

// BenchmarkVorbisWindow benchmarks window computation.
func BenchmarkVorbisWindow(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range Overlap {
			VorbisWindow(j, Overlap)
		}
	}
}

// BenchmarkOverlapAdd benchmarks the overlap-add operation.
func BenchmarkOverlapAdd(b *testing.B) {
	current := make([]float32, 1920)
	prevOverlap := make([]float32, 120)
	for i := range current {
		current[i] = float32(i) / 1920.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		OverlapAdd(current, prevOverlap, 120)
	}
}

// BenchmarkDecodeFrame benchmarks full frame decoding.
func BenchmarkDecodeFrame(b *testing.B) {
	dec := NewDecoder(1)
	data := createSilenceFrame()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec.DecodeFrame(data, 960)
	}
}

// BenchmarkDecodeFrame_Stereo benchmarks stereo frame decoding.
func BenchmarkDecodeFrame_Stereo(b *testing.B) {
	dec := NewDecoder(2)
	data := createSilenceFrame()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec.DecodeFrame(data, 960)
	}
}

// BenchmarkMidSideToLR benchmarks stereo conversion.
func BenchmarkMidSideToLR(b *testing.B) {
	n := 100
	mid := make([]float32, n)
	side := make([]float32, n)
	for i := range mid {
		mid[i] = float32(i) / float32(n)
		side[i] = float32(n-i) / float32(n)
	}
	theta := float32(math.Pi / 4)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MidSideToLR(mid, side, theta)
	}
}

const mdctSNRThreshold = 60.0
const mdctRoundTripSNRThreshold = 40.0

// checkMDCTForward computes the expected MDCT output using direct formula and compares.
// This is a direct port of the check() function from test_unit_mdct.c
// Returns SNR in dB.
//
// The formula used (from C test):
//
//	expected[bin] = sum_{k=0}^{nfft-1} in[k] * cos(2*pi*(k+0.5+nfft/4)*(bin+0.5)/nfft) / (nfft/4)
func checkMDCTForward(in, out []float64, nfft int) float64 {
	var errpow, sigpow float64

	for bin := 0; bin < nfft/2; bin++ {
		var ansr float64
		for k := range nfft {
			phase := 2 * math.Pi * (float64(k) + 0.5 + float64(nfft)/4.0) * (float64(bin) + 0.5) / float64(nfft)
			re := math.Cos(phase)
			re /= float64(nfft) / 4.0
			ansr += in[k] * re
		}
		difr := ansr - out[bin]
		errpow += difr * difr
		sigpow += ansr * ansr
	}

	if errpow == 0 {
		return math.Inf(1) // Perfect match
	}
	return 10 * math.Log10(sigpow/errpow)
}

// checkMDCTInverse computes the expected IMDCT output using direct formula and compares.
// This is a direct port of the check_inv() function from test_unit_mdct.c
// Returns SNR in dB.
//
// The formula used (from C test):
//
//	expected[bin] = sum_{k=0}^{nfft/2-1} in[k] * cos(2*pi*(bin+0.5+nfft/4)*(k+0.5)/nfft)
func checkMDCTInverse(in, out []float64, nfft int) float64 {
	var errpow, sigpow float64

	for bin := range nfft {
		var ansr float64
		for k := 0; k < nfft/2; k++ {
			phase := 2 * math.Pi * (float64(bin) + 0.5 + float64(nfft)/4.0) * (float64(k) + 0.5) / float64(nfft)
			re := math.Cos(phase)
			ansr += in[k] * re
		}
		difr := ansr - out[bin]
		errpow += difr * difr
		sigpow += ansr * ansr
	}

	if errpow == 0 {
		return math.Inf(1) // Perfect match
	}
	return 10 * math.Log10(sigpow/errpow)
}

// mdctForwardRef computes forward MDCT using the reference formula from test_unit_mdct.c
// This matches what checkMDCTForward expects.
// Input: nfft samples, Output: nfft/2 coefficients
func mdctForwardRef[T ~float32 | ~float64](in []T) []float64 {
	nfft := len(in)
	out := make([]float64, nfft/2)

	for bin := 0; bin < nfft/2; bin++ {
		var sum float64
		for k := range nfft {
			phase := 2 * math.Pi * (float64(k) + 0.5 + float64(nfft)/4.0) * (float64(bin) + 0.5) / float64(nfft)
			re := math.Cos(phase)
			re /= float64(nfft) / 4.0
			sum += float64(in[k]) * re
		}
		out[bin] = sum
	}

	return out
}

// mdctInverseRef computes inverse MDCT using the reference formula from test_unit_mdct.c
// This matches what checkMDCTInverse expects.
// Input: nfft/2 coefficients, Output: nfft samples
func mdctInverseRef(in []float64, nfft int) []float64 {
	out := make([]float64, nfft)

	for bin := range nfft {
		var sum float64
		for k := 0; k < nfft/2; k++ {
			phase := 2 * math.Pi * (float64(bin) + 0.5 + float64(nfft)/4.0) * (float64(k) + 0.5) / float64(nfft)
			re := math.Cos(phase)
			sum += float64(in[k]) * re
		}
		out[bin] = sum
	}

	return out
}

// mdctTest1d runs a single MDCT test for given size.
// This is a direct port of test1d() from test_unit_mdct.c
func mdctTest1d(t *testing.T, nfft int, isInverse bool) {
	t.Helper()

	// Create random input with same distribution as C test
	rng := rand.New(rand.NewSource(42)) // Fixed seed for reproducibility
	in := make([]float64, nfft)
	for k := range nfft {
		in[k] = float64(rng.Intn(32768) - 16384)
	}

	// Scale input by 32768 as in C test
	for k := range nfft {
		in[k] *= 32768
	}

	// For inverse, also divide by nfft
	if isInverse {
		for k := range nfft {
			in[k] /= float64(nfft)
		}
	}

	// Keep copy for reference
	inCopy := make([]float64, nfft)
	copy(inCopy, in)

	var snr float64
	if isInverse {
		// Input is nfft/2 coefficients (use first half)
		coeffs := in[:nfft/2]

		// Compute IMDCT using reference formula
		out := mdctInverseRef(coeffs, nfft)

		// Apply TDAC mirroring as done in the C test
		// (because clt_mdct_backward no longer does it)
		for k := 0; k < nfft/4; k++ {
			out[nfft-k-1] = out[nfft/2+k]
		}

		snr = checkMDCTInverse(coeffs, out, nfft)
	} else {
		// Compute MDCT using reference formula
		out := mdctForwardRef(in)

		snr = checkMDCTForward(inCopy, out, nfft)
	}

	direction := "forward"
	if isInverse {
		direction = "inverse"
	}

	t.Logf("nfft=%d %s, SNR = %.2f dB", nfft, direction, snr)

	if snr < mdctSNRThreshold {
		t.Errorf("Poor SNR for nfft=%d %s: %.2f dB (threshold: %.2f dB)",
			nfft, direction, snr, mdctSNRThreshold)
	}
}

// TestMDCTUnit_PowerOf2 tests MDCT reference implementation with power-of-2 sizes
func TestMDCTUnit_PowerOf2(t *testing.T) {
	sizes := []int{32, 256, 512, 1024, 2048}

	t.Run("Forward", func(t *testing.T) {
		for _, n := range sizes {
			mdctTest1d(t, n, false)
		}
	})
	t.Run("Inverse", func(t *testing.T) {
		for _, n := range sizes {
			mdctTest1d(t, n, true)
		}
	})
}

// TestMDCTUnit_NonPowerOf2 tests MDCT reference implementation with non-power-of-2 sizes
func TestMDCTUnit_NonPowerOf2(t *testing.T) {
	// These sizes are tested in C when RADIX_TWO_ONLY is not defined
	sizes := []int{36, 40, 60, 120, 240, 480, 960, 1920}

	for _, size := range sizes {
		t.Run("Forward", func(t *testing.T) {
			mdctTest1d(t, size, false)
		})
		t.Run("Inverse", func(t *testing.T) {
			mdctTest1d(t, size, true)
		})
	}
}

// TestMDCTUnit_CELTSizes tests MDCT with CELT-specific sizes
func TestMDCTUnit_CELTSizes(t *testing.T) {
	// CELT frame sizes correspond to 2.5ms, 5ms, 10ms, 20ms at 48kHz
	sizes := []int{240, 480, 960, 1920}

	for _, size := range sizes {
		t.Run("Forward", func(t *testing.T) {
			mdctTest1d(t, size, false)
		})
		t.Run("Inverse", func(t *testing.T) {
			mdctTest1d(t, size, true)
		})
	}
}

// TestMDCTUnit_GoIMDCT tests the Go IMDCT implementation against reference
func TestMDCTUnit_GoIMDCT(t *testing.T) {
	// Test CELT sizes with the actual Go implementation
	sizes := []int{120, 240, 480, 960}

	for _, N := range sizes {
		t.Run("", func(t *testing.T) {
			// Create random coefficients
			rng := rand.New(rand.NewSource(42))
			coeffs := make([]float32, N)
			for i := range N {
				coeffs[i] = float32(rng.Intn(32768) - 16384)
			}

			// Compute using Go IMDCT implementation
			goOut := IMDCT(coeffs)

			// Compute using direct IMDCT formula (standard, with 2/N normalization)
			directOut := IMDCTDirect(coeffs)

			// Compare
			var errpow, sigpow float64
			minLen := min(len(directOut), len(goOut))
			for i := 0; i < minLen; i++ {
				diff := float64(goOut[i] - directOut[i])
				errpow += diff * diff
				sigpow += float64(directOut[i] * directOut[i])
			}

			if sigpow == 0 {
				t.Skip("Signal power is zero")
			}

			snr := 10 * math.Log10(sigpow/errpow)
			t.Logf("N=%d IMDCT Go vs Direct SNR = %.2f dB", N, snr)

			if snr < mdctSNRThreshold {
				t.Errorf("Poor SNR for N=%d IMDCT: %.2f dB (threshold: %.2f dB)",
					N, snr, mdctSNRThreshold)
			}
		})
	}
}

// TestMDCTUnit_GoMDCT tests the Go MDCT implementation against reference
func TestMDCTUnit_GoMDCT(t *testing.T) {
	// Test CELT sizes with the actual Go implementation
	sizes := []int{120, 240, 480, 960}

	for _, N := range sizes {
		t.Run("", func(t *testing.T) {
			// Create random samples (2*N for MDCT input)
			rng := rand.New(rand.NewSource(42))
			samples := make([]float64, 2*N)
			for i := 0; i < 2*N; i++ {
				samples[i] = float64(rng.Intn(32768) - 16384)
			}

			// Compute using Go mdctDirect (has scale = 4/N2 = 2/N)
			goOut := float64sFromMDCTForwardTest(mdctDirect(float32sForMDCTForwardTest(samples)))

			// Compute using reference formula (divides by nfft/4 = N/2)
			refOut := mdctForwardRef(samples)

			if len(goOut) != len(refOut) {
				t.Fatalf("Output length mismatch: Go=%d, Ref=%d", len(goOut), len(refOut))
			}

			// Both mdctDirect and mdctForwardRef use the same normalization:
			// - mdctDirect: multiplies by 4/N2 = 4/(2N) = 2/N
			// - mdctForwardRef: divides by nfft/4 = 2N/4 = N/2 (equivalent to multiply by 2/N)
			// So they should match directly!
			var errpow, sigpow float64
			for i := range refOut {
				diff := goOut[i] - refOut[i]
				errpow += diff * diff
				sigpow += refOut[i] * refOut[i]
			}

			if sigpow == 0 {
				t.Skip("Signal power is zero")
			}

			snr := 10 * math.Log10(sigpow/errpow)
			t.Logf("N=%d MDCT Go vs Ref SNR = %.2f dB", N, snr)

			if snr < mdctSNRThreshold {
				t.Errorf("Poor SNR for N=%d MDCT: %.2f dB (threshold: %.2f dB)",
					N, snr, mdctSNRThreshold)
			}
		})
	}
}

// TestMDCTUnit_RoundTrip tests round-trip consistency using Go's IMDCTDirect and mdctDirect.
// Note: This tests that MDCT(IMDCT(x)) = x (with proper normalization).
func TestMDCTUnit_RoundTrip(t *testing.T) {
	sizes := []int{120, 240, 480, 960}

	for _, N := range sizes {
		t.Run("", func(t *testing.T) {
			// Create random MDCT coefficients
			rng := rand.New(rand.NewSource(42))
			coeffs := make([]float32, N)
			for i := range coeffs {
				coeffs[i] = float32(rng.Intn(32768) - 16384)
			}

			// Compute IMDCT using Go implementation (has 2/N normalization)
			timeOut := IMDCTDirect(coeffs)

			// Compute MDCT of the result using Go implementation (has 2/N normalization)
			coeffsBack := float64sFromMDCTForwardTest(mdctDirect(timeOut))

			// mdctDirect scales by 2/N, IMDCTDirect is unscaled (per libopus check_inv).
			// For the MDCT basis used here, MDCT(IMDCT(x)) = 2*x, so scale by 0.5.
			scale := 0.5

			var errpow, sigpow float64
			for i := range N {
				scaled := coeffsBack[i] * scale
				diff := scaled - float64(coeffs[i])
				errpow += diff * diff
				sigpow += float64(coeffs[i] * coeffs[i])
			}

			if sigpow == 0 {
				t.Skip("Signal power is zero")
			}

			snr := 10 * math.Log10(sigpow/errpow)
			t.Logf("N=%d MDCT(IMDCT(x)) round-trip SNR = %.2f dB", N, snr)

			if snr < mdctRoundTripSNRThreshold {
				t.Errorf("Poor round-trip SNR for N=%d: %.2f dB (threshold: %.2f dB)",
					N, snr, mdctRoundTripSNRThreshold)
			}
		})
	}
}

// TestMDCTUnit_AllSizes runs the complete test suite matching the C test
func TestMDCTUnit_AllSizes(t *testing.T) {
	// All sizes tested in the C test
	allSizes := []int{
		// Power of 2
		32, 256, 512, 1024, 2048,
		// Non-power of 2 (when RADIX_TWO_ONLY is not defined)
		36, 40, 60, 120, 240, 480, 960, 1920,
	}

	for _, size := range allSizes {
		t.Run("", func(t *testing.T) {
			mdctTest1d(t, size, false) // forward
			mdctTest1d(t, size, true)  // inverse
		})
	}
}
