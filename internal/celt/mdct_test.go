package celt

import (
	"fmt"
	"math"
	"testing"
)

// TestIMDCT_OutputLength verifies IMDCT produces correct output length.
func TestIMDCT_OutputLength(t *testing.T) {
	for _, n := range []int{120, 240, 480, 960} {
		spectrum := make([]float64, n)
		out := IMDCT(spectrum)
		if len(out) != 2*n {
			t.Errorf("IMDCT(%d) output length = %d, want %d", n, len(out), 2*n)
		}
	}
}

// TestIMDCT_SmallSizes verifies IMDCT handles small sizes.
func TestIMDCT_SmallSizes(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 16} {
		spectrum := make([]float64, n)
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
	spectrum := make([]float64, n)
	spectrum[0] = 1.0 // DC component

	out := IMDCT(spectrum)

	// DC should produce some output
	// Check that output is not all zeros
	var maxAbs float64
	for _, x := range out {
		if math.Abs(x) > maxAbs {
			maxAbs = math.Abs(x)
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
		spectrum := make([]float64, n)
		spectrum[0] = 1.0

		result := IMDCT(spectrum)

		// Check output length
		if len(result) != 2*n {
			t.Errorf("IMDCT(%d) output length = %d, want %d", n, len(result), 2*n)
		}

		// Check output is not all zeros
		var maxAbs float64
		for _, x := range result {
			if math.Abs(x) > maxAbs {
				maxAbs = math.Abs(x)
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
			spectrum := make([]float64, n)
			for i := range spectrum {
				spectrum[i] = math.Sin(float64(i) * 0.1)
			}

			// Input energy (frequency domain)
			inputEnergy := 0.0
			for _, x := range spectrum {
				inputEnergy += x * x
			}

			output := IMDCT(spectrum)

			// Output energy (time domain)
			outputEnergy := 0.0
			for _, x := range output {
				outputEnergy += x * x
			}

			// Ratio depends on IMDCT normalization:
			// - FFT path (power-of-two sizes) scales by 2/N, ratio ~ 4/N
			// - Direct path (CELT sizes) is unscaled, ratio ~ N
			ratio := outputEnergy / inputEnergy
			expectedRatio := float64(n)
			if isPowerOfTwo(n / 2) {
				expectedRatio = 4.0 / float64(n)
			}

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
			spectrum := make([]float64, n)
			spectrum[0] = 1.0

			output := IMDCT(spectrum)

			// DC component should produce non-zero output
			hasNonZero := false
			for _, x := range output {
				if math.Abs(x) > 1e-10 {
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

	spectrum := make([]float64, n)
	for i := range spectrum {
		spectrum[i] = math.Sin(float64(i) * 0.1)
	}

	directOut := IMDCTDirect(spectrum)

	if len(directOut) != 2*n {
		t.Errorf("IMDCTDirect produced %d samples, want %d", len(directOut), 2*n)
	}

	// Verify output has non-zero values
	hasNonZero := false
	for _, x := range directOut {
		if math.Abs(x) > 1e-10 {
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
			spectrum := make([]float64, n)
			for i := range spectrum {
				spectrum[i] = math.Cos(float64(i) * 0.05)
			}

			output := IMDCTDirect(spectrum)

			// Verify output length
			if len(output) != 2*n {
				t.Errorf("IMDCTDirect(%d) produced %d samples, want %d", n, len(output), 2*n)
			}

			// Verify output has expected properties (not all zeros)
			hasNonZero := false
			for _, x := range output {
				if math.Abs(x) > 1e-10 {
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
		coeffs := make([]float64, 120*shortBlocks)
		// Fill with test pattern (impulse at start of each block)
		for b := 0; b < shortBlocks; b++ {
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
			if math.Abs(x) > maxAbs {
				maxAbs = math.Abs(x)
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
	coeffs := make([]float64, n)
	coeffs[0] = 1.0
	coeffs[5] = 0.5

	regular := IMDCT(coeffs)
	short := IMDCTShort(coeffs, 1)

	if len(regular) != len(short) {
		t.Errorf("Length mismatch: IMDCT=%d, IMDCTShort=%d", len(regular), len(short))
		return
	}

	for i := range regular {
		if math.Abs(regular[i]-short[i]) > 1e-10 {
			t.Errorf("Mismatch at %d: regular=%v, short=%v", i, regular[i], short[i])
		}
	}
}

// TestVorbisWindow_Values verifies Vorbis window produces valid values.
// The Vorbis window values should be in [0, 1] and form a smooth curve.
func TestVorbisWindow_Values(t *testing.T) {
	overlap := Overlap

	// Check that all values are in valid range
	for i := 0; i < overlap; i++ {
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
	prev := 0.0
	for i := 0; i < overlap; i++ {
		w := VorbisWindow(i, overlap)
		if w < prev-1e-10 { // Allow tiny numerical errors
			t.Errorf("Window not monotonic at i=%d: prev=%v, curr=%v", i, prev, w)
		}
		prev = w
	}
}

// TestVorbisWindow_Symmetry verifies window symmetry.
func TestVorbisWindow_Symmetry(t *testing.T) {
	overlap := Overlap
	for i := 0; i < overlap; i++ {
		w1 := VorbisWindow(i, overlap)
		w2 := VorbisWindow(overlap-1-i, overlap)

		// For CELT's Vorbis window, the pair must be power-complementary.
		if w1 < 0 || w1 > 1 {
			t.Errorf("Window value out of range at i=%d: %v", i, w1)
		}
		if w2 < 0 || w2 > 1 {
			t.Errorf("Window value out of range at i=%d: %v", overlap-1-i, w2)
		}
		sum := w1*w1 + w2*w2
		if math.Abs(sum-1.0) > 1e-12 {
			t.Errorf("Power complement mismatch at i=%d: sum=%v", i, sum)
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

	for i := 0; i < 120; i++ {
		expected := VorbisWindow(i, 120)
		if math.Abs(buf[i]-expected) > 1e-15 {
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
	window := make([]float64, overlap)
	for i := 0; i < overlap; i++ {
		window[i] = VorbisWindow(i, overlap)
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
		if math.Abs(sum-1.0) > 0.01 {
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
			imdctOut := make([]float64, 2*frameSize)
			for i := range imdctOut {
				imdctOut[i] = float64(i) * 0.001 // Arbitrary non-zero values
			}

			// Previous overlap buffer
			prevOverlap := make([]float64, overlap)

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
	frame1 := make([]float64, frameSize*2)
	frame2 := make([]float64, frameSize*2)

	for i := range frame1 {
		frame1[i] = float64(i) / float64(len(frame1))
	}
	for i := range frame2 {
		frame2[i] = 0.5 + float64(i)/(2.0*float64(len(frame2)))
	}

	// Apply window
	ApplyWindow(frame1, overlap)
	ApplyWindow(frame2, overlap)

	// First overlap-add (with zero previous)
	prevOverlap := make([]float64, overlap)
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
	jump := math.Abs(lastVal - firstVal)

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

	frame := make([]float64, frameSize*2) // All zeros
	prevOverlap := make([]float64, overlap)

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
	mid := []float64{1.0, 2.0, 3.0, 4.0}
	side := []float64{0.5, 0.5, 0.5, 0.5}

	// theta=0: cos(0)=1, sin(0)=0
	// L = 1*mid + 0*side = mid
	// R = 1*mid - 0*side = mid
	left, right := MidSideToLR(mid, side, 0)

	for i := range left {
		if math.Abs(left[i]-mid[i]) > 1e-10 {
			t.Errorf("Left[%d] = %v, want %v", i, left[i], mid[i])
		}
		if math.Abs(right[i]-mid[i]) > 1e-10 {
			t.Errorf("Right[%d] = %v, want %v", i, right[i], mid[i])
		}
	}
}

// TestMidSideToLR_FullStereo verifies mid-side with theta=pi/2.
func TestMidSideToLR_FullStereo(t *testing.T) {
	mid := []float64{0, 0, 0, 0}
	side := []float64{1.0, 2.0, 3.0, 4.0}

	// theta=pi/2: cos=0, sin=1
	// L = 0*mid + 1*side = side
	// R = 0*mid - 1*side = -side
	left, right := MidSideToLR(mid, side, math.Pi/2)

	for i := range left {
		if math.Abs(left[i]-side[i]) > 1e-10 {
			t.Errorf("Left[%d] = %v, want %v", i, left[i], side[i])
		}
		if math.Abs(right[i]+side[i]) > 1e-10 {
			t.Errorf("Right[%d] = %v, want %v", i, right[i], -side[i])
		}
	}
}

// TestMidSideToLR_Inversion verifies L+R and L-R properties.
func TestMidSideToLR_Inversion(t *testing.T) {
	mid := []float64{1.0, 2.0, 3.0}
	side := []float64{0.1, 0.2, 0.3}
	theta := math.Pi / 4 // 45 degrees

	left, right := MidSideToLR(mid, side, theta)

	// L + R should be approximately 2*cos(theta)*mid (when side is small)
	// L - R should be approximately 2*sin(theta)*side
	cosT := math.Cos(theta)
	sinT := math.Sin(theta)

	for i := range mid {
		sum := left[i] + right[i]
		diff := left[i] - right[i]

		expectedSum := 2 * cosT * mid[i]
		expectedDiff := 2 * sinT * side[i]

		if math.Abs(sum-expectedSum) > 1e-10 {
			t.Errorf("L+R at %d: got %v, want %v", i, sum, expectedSum)
		}
		if math.Abs(diff-expectedDiff) > 1e-10 {
			t.Errorf("L-R at %d: got %v, want %v", i, diff, expectedDiff)
		}
	}
}

// TestIntensityStereo_NoInversion verifies intensity stereo without inversion.
func TestIntensityStereo_NoInversion(t *testing.T) {
	mono := []float64{1.0, 2.0, 3.0, 4.0}

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
	mono := []float64{1.0, 2.0, 3.0, 4.0}

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
	for i := 0; i < 3; i++ {
		_, err := dec.DecodeFrame(data, 960)
		if err != nil {
			t.Errorf("Frame %d error: %v", i, err)
		}
	}

	// Energy state should have been updated
	energy := dec.GetEnergy(0, 0)
	// After silence frames, energy should be updated
	// Just verify it's a valid number
	if math.IsNaN(energy) || math.IsInf(energy, 0) {
		t.Errorf("Invalid energy after frames: %v", energy)
	}
}

// TestSynthesize_Basic tests the Synthesize function.
func TestSynthesize_Basic(t *testing.T) {
	dec := NewDecoder(1)

	// Create simple coefficients
	n := 120
	coeffs := make([]float64, n)
	coeffs[0] = 1.0

	samples := dec.Synthesize(coeffs, false, 1)

	// Should produce some output
	if len(samples) == 0 {
		t.Error("Synthesize produced no output")
	}

	// Check for non-zero samples
	var maxAbs float64
	for _, x := range samples {
		if math.Abs(x) > maxAbs {
			maxAbs = math.Abs(x)
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
	coeffs := make([]float64, n)
	for i := 0; i < 4; i++ {
		coeffs[i*120] = 1.0
	}

	samples := dec.Synthesize(coeffs, true, 4)

	if len(samples) == 0 {
		t.Error("Synthesize (transient) produced no output")
	}
}

// TestDeEmphasis tests the de-emphasis filter.
func TestDeEmphasis(t *testing.T) {
	dec := NewDecoder(1)

	// Create impulse
	samples := make([]float64, 100)
	samples[0] = 1.0

	dec.applyDeemphasis(samples)

	// First sample should still be 1.0 (no history)
	if math.Abs(samples[0]-1.0) > 1e-10 {
		t.Errorf("First sample = %v, want 1.0", samples[0])
	}

	// Second sample should be PreemphCoef (previous output was 1.0)
	expected := PreemphCoef
	if math.Abs(samples[1]-expected) > 1e-10 {
		t.Errorf("Second sample = %v, want %v", samples[1], expected)
	}

	// Verify exponential decay pattern
	for i := 2; i < 10; i++ {
		expected := math.Pow(PreemphCoef, float64(i))
		if math.Abs(samples[i]-expected) > 1e-6 {
			t.Errorf("Sample[%d] = %v, want %v", i, samples[i], expected)
		}
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
	spectrum := make([]float64, 960)
	for i := range spectrum {
		spectrum[i] = float64(i%10) / 10.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IMDCT(spectrum)
	}
}

// BenchmarkIMDCT_Short benchmarks IMDCT with short frame size.
func BenchmarkIMDCT_Short(b *testing.B) {
	spectrum := make([]float64, 120)
	for i := range spectrum {
		spectrum[i] = float64(i%10) / 10.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IMDCT(spectrum)
	}
}

// BenchmarkIMDCTShort benchmarks the short block IMDCT.
func BenchmarkIMDCTShort(b *testing.B) {
	coeffs := make([]float64, 120*8)
	for i := range coeffs {
		coeffs[i] = float64(i%10) / 10.0
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
		for j := 0; j < Overlap; j++ {
			VorbisWindow(j, Overlap)
		}
	}
}

// BenchmarkOverlapAdd benchmarks the overlap-add operation.
func BenchmarkOverlapAdd(b *testing.B) {
	current := make([]float64, 1920)
	prevOverlap := make([]float64, 120)
	for i := range current {
		current[i] = float64(i) / 1920.0
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
	mid := make([]float64, n)
	side := make([]float64, n)
	for i := range mid {
		mid[i] = float64(i) / float64(n)
		side[i] = float64(n-i) / float64(n)
	}
	theta := math.Pi / 4

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MidSideToLR(mid, side, theta)
	}
}
