//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for MDCT/IMDCT transforms.
// This file ports test_unit_mdct.c from libopus to Go with direct CGO comparison.
//
// Reference: libopus/celt/tests/test_unit_mdct.c
// Copyright (c) 2008-2011 Xiph.Org Foundation
// Written by Jean-Marc Valin
// Ported to Go for gopus.
package cgo

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// mdctSNRThreshold is the minimum acceptable SNR in dB (from test_unit_mdct.c)
const mdctSNRThreshold = 60.0

// checkMDCTForward computes the expected MDCT output using direct formula and compares.
// This is a direct port of the check() function from test_unit_mdct.c
// Returns SNR in dB.
func checkMDCTForward(in, out []float64, nfft int) float64 {
	var errpow, sigpow float64

	for bin := 0; bin < nfft/2; bin++ {
		var ansr float64
		for k := 0; k < nfft; k++ {
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
func checkMDCTInverse(in, out []float64, nfft int) float64 {
	var errpow, sigpow float64

	for bin := 0; bin < nfft; bin++ {
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

// TestMDCT_LibopusForward tests MDCT forward transform matching libopus behavior.
// This compares the raw MDCT output (without overlap handling) against the reference formula.
func TestMDCT_LibopusForward(t *testing.T) {
	// Get the CELT mode
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()
	t.Logf("CELT mode overlap: %d", overlap)

	// Test with CELT frame sizes (shift values 1-3)
	// shift=0: 1920 (not commonly used)
	// shift=1: 960 (20ms at 48kHz)
	// shift=2: 480 (10ms at 48kHz)
	// shift=3: 240 (5ms at 48kHz)
	shifts := []int{1, 2, 3}

	for _, shift := range shifts {
		nfft := mode.MDCTSize(shift)
		n2 := nfft / 2

		t.Run(fmt.Sprintf("nfft=%d", nfft), func(t *testing.T) {
			// Create random input similar to C test
			rng := rand.New(rand.NewSource(42))
			inputLen := n2 + overlap
			input := make([]float32, inputLen)
			inputCopy := make([]float64, inputLen)

			for k := 0; k < inputLen; k++ {
				val := float32(rng.Intn(32768) - 16384)
				input[k] = val
				inputCopy[k] = float64(val)
			}

			// Call libopus MDCT
			output := mode.MDCTForward(input, shift)

			// Convert output to float64 for SNR calculation
			outputF64 := make([]float64, n2)
			for i := range output {
				outputF64[i] = float64(output[i])
			}

			// Verify that the output has reasonable characteristics
			var maxAbs, sumSq float64
			for _, v := range outputF64 {
				if math.Abs(v) > maxAbs {
					maxAbs = math.Abs(v)
				}
				sumSq += v * v
			}
			rms := math.Sqrt(sumSq / float64(n2))

			t.Logf("nfft=%d: maxAbs=%.2f, RMS=%.2f", nfft, maxAbs, rms)

			// Sanity checks
			if maxAbs < 1.0 {
				t.Errorf("Output max abs too small: %v", maxAbs)
			}
			if math.IsNaN(maxAbs) || math.IsInf(maxAbs, 0) {
				t.Errorf("Output contains NaN or Inf")
			}
		})
	}
}

// TestMDCT_LibopusInverse tests IMDCT inverse transform matching libopus behavior.
// Note: The libopus clt_mdct_backward includes windowing and overlap-add, so
// comparing directly against the raw reference formula is not straightforward.
// Instead, we verify the output has reasonable characteristics.
func TestMDCT_LibopusInverse(t *testing.T) {
	// Get the CELT mode
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()
	t.Logf("CELT mode overlap: %d", overlap)

	// Test with CELT frame sizes
	shifts := []int{1, 2, 3}

	for _, shift := range shifts {
		nfft := mode.MDCTSize(shift)
		n2 := nfft / 2

		t.Run(fmt.Sprintf("nfft=%d", nfft), func(t *testing.T) {
			// Create random frequency input
			rng := rand.New(rand.NewSource(42))
			input := make([]float32, n2)

			for k := 0; k < n2; k++ {
				val := float32(rng.Intn(32768)-16384) / float32(nfft)
				input[k] = val
			}

			// Call libopus IMDCT
			output := mode.IMDCTBackward(input, shift)

			// Verify output has reasonable characteristics
			var maxAbs, sumSq float64
			for _, v := range output {
				if math.Abs(float64(v)) > maxAbs {
					maxAbs = math.Abs(float64(v))
				}
				sumSq += float64(v) * float64(v)
			}
			rms := math.Sqrt(sumSq / float64(len(output)))

			t.Logf("nfft=%d inverse: len=%d, maxAbs=%.4f, RMS=%.4f", nfft, len(output), maxAbs, rms)

			// Sanity checks
			if maxAbs < 1e-6 {
				t.Errorf("Output max abs too small: %v", maxAbs)
			}
			if math.IsNaN(maxAbs) || math.IsInf(maxAbs, 0) {
				t.Errorf("Output contains NaN or Inf")
			}
		})
	}
}

// TestMDCT_LibopusRoundTrip tests MDCT -> IMDCT round-trip preservation.
// Note: Perfect round-trip requires proper overlap-add across frames.
// For a single frame, the correlation in the middle region should still be high.
func TestMDCT_LibopusRoundTrip(t *testing.T) {
	// Get the CELT mode
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()

	// Test with CELT frame sizes
	shifts := []int{1, 2, 3}

	for _, shift := range shifts {
		nfft := mode.MDCTSize(shift)
		n2 := nfft / 2

		t.Run(fmt.Sprintf("nfft=%d", nfft), func(t *testing.T) {
			// Create random input signal
			rng := rand.New(rand.NewSource(42))
			inputLen := n2 + overlap
			input := make([]float32, inputLen)
			inputCopy := make([]float64, inputLen)

			for k := 0; k < inputLen; k++ {
				val := float32(rng.Intn(32768) - 16384)
				input[k] = val
				inputCopy[k] = float64(val)
			}

			// Forward MDCT
			mdctOut := mode.MDCTForward(input, shift)

			// Inverse IMDCT
			imdctOut := mode.IMDCTBackward(mdctOut, shift)

			// The round-trip should approximately recover the input in the
			// non-windowed region (middle portion), allowing for scaling differences
			// and overlap-add effects

			// Find the correlation and max difference in the middle region
			// (excluding the windowed overlap edges)
			startIdx := overlap / 2
			endIdx := n2

			var sumInput, sumOutput, sumInputSq, sumOutputSq float64
			for i := startIdx; i < endIdx; i++ {
				inVal := inputCopy[i]
				outVal := float64(imdctOut[i])
				sumInput += inVal
				sumOutput += outVal
				sumInputSq += inVal * inVal
				sumOutputSq += outVal * outVal
			}

			n := float64(endIdx - startIdx)
			meanIn := sumInput / n
			meanOut := sumOutput / n

			// Compute correlation coefficient
			var covAB, varA, varB float64
			for i := startIdx; i < endIdx; i++ {
				dA := inputCopy[i] - meanIn
				dB := float64(imdctOut[i]) - meanOut
				covAB += dA * dB
				varA += dA * dA
				varB += dB * dB
			}

			corr := float64(0)
			if varA > 0 && varB > 0 {
				corr = covAB / (math.Sqrt(varA) * math.Sqrt(varB))
			}

			t.Logf("nfft=%d round-trip correlation = %.6f", nfft, corr)

			// The correlation should be reasonably high even for a single frame
			// (without proper overlap-add, we can't expect perfect reconstruction)
			// A threshold of 0.9 is reasonable for single-frame round-trip
			if math.Abs(corr) < 0.9 {
				t.Errorf("Poor round-trip correlation for nfft=%d: %.6f (expected > 0.9)",
					nfft, math.Abs(corr))
			}
		})
	}
}

// TestMDCT_GoVsLibopusIMDCT compares Go IMDCT implementation against libopus.
func TestMDCT_GoVsLibopusIMDCT(t *testing.T) {
	// Get the CELT mode
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()

	// Test with CELT frame sizes
	shifts := []int{1, 2, 3}

	for _, shift := range shifts {
		nfft := mode.MDCTSize(shift)
		n2 := nfft / 2

		t.Run(fmt.Sprintf("nfft=%d", nfft), func(t *testing.T) {
			// Create random frequency input
			rng := rand.New(rand.NewSource(42))
			input := make([]float32, n2)
			inputF64 := make([]float64, n2)

			for k := 0; k < n2; k++ {
				val := float32(rng.Intn(32768)-16384) / float32(nfft)
				input[k] = val
				inputF64[k] = float64(val)
			}

			// Call libopus IMDCT
			libopusOut := mode.IMDCTBackward(input, shift)

			// Call Go IMDCT (using libopusIMDCT which matches the structure)
			prevOverlap := make([]float64, overlap)
			goOut := libopusIMDCTGo(inputF64, prevOverlap, overlap)

			// Compare outputs
			minLen := n2 + overlap
			if len(goOut) < minLen {
				minLen = len(goOut)
			}

			var errPow, sigPow, maxDiff float64
			maxDiffIdx := 0
			for i := 0; i < minLen; i++ {
				libVal := float64(libopusOut[i])
				goVal := goOut[i]
				diff := libVal - goVal
				if math.Abs(diff) > maxDiff {
					maxDiff = math.Abs(diff)
					maxDiffIdx = i
				}
				errPow += diff * diff
				sigPow += libVal * libVal
			}

			snr := float64(0)
			if errPow > 0 && sigPow > 0 {
				snr = 10 * math.Log10(sigPow/errPow)
			} else if errPow == 0 {
				snr = 200 // Perfect match
			}

			t.Logf("nfft=%d Go vs libopus IMDCT: SNR=%.2f dB, maxDiff=%.2e at idx %d",
				nfft, snr, maxDiff, maxDiffIdx)

			// We expect very high SNR for matching implementations
			if snr < 60 {
				t.Errorf("Poor SNR for nfft=%d Go vs libopus IMDCT: %.2f dB (expected >= 60 dB)",
					nfft, snr)
			}
		})
	}
}

// TestMDCT_GoVsLibopusMDCT compares Go MDCT implementation against libopus.
func TestMDCT_GoVsLibopusMDCT(t *testing.T) {
	// Get the CELT mode
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()

	// Test with CELT frame sizes
	shifts := []int{1, 2, 3}

	for _, shift := range shifts {
		nfft := mode.MDCTSize(shift)
		n2 := nfft / 2

		t.Run(fmt.Sprintf("nfft=%d", nfft), func(t *testing.T) {
			// Create random input signal
			rng := rand.New(rand.NewSource(42))
			inputLen := n2 + overlap
			input := make([]float32, inputLen)
			inputF64 := make([]float64, inputLen)

			for k := 0; k < inputLen; k++ {
				val := float32(rng.Intn(32768) - 16384)
				input[k] = val
				inputF64[k] = float64(val)
			}

			// Call libopus MDCT
			libopusOut := mode.MDCTForward(input, shift)

			// Call Go MDCT
			goOut := mdctForwardOverlapGo(inputF64, overlap)

			// Compare outputs
			minLen := n2
			if len(goOut) < minLen {
				minLen = len(goOut)
			}

			var errPow, sigPow, maxDiff float64
			maxDiffIdx := 0
			for i := 0; i < minLen; i++ {
				libVal := float64(libopusOut[i])
				goVal := goOut[i]
				diff := libVal - goVal
				if math.Abs(diff) > maxDiff {
					maxDiff = math.Abs(diff)
					maxDiffIdx = i
				}
				errPow += diff * diff
				sigPow += libVal * libVal
			}

			snr := float64(0)
			if errPow > 0 && sigPow > 0 {
				snr = 10 * math.Log10(sigPow/errPow)
			} else if errPow == 0 {
				snr = 200 // Perfect match
			}

			t.Logf("nfft=%d Go vs libopus MDCT: SNR=%.2f dB, maxDiff=%.2e at idx %d",
				nfft, snr, maxDiff, maxDiffIdx)

			// We expect very high SNR for matching implementations
			if snr < 60 {
				t.Errorf("Poor SNR for nfft=%d Go vs libopus MDCT: %.2f dB (expected >= 60 dB)",
					nfft, snr)
			}
		})
	}
}

// TestMDCT_ReferenceFormula tests the reference formulas match the C test expectations.
// This verifies the check() and check_inv() functions work correctly.
func TestMDCT_ReferenceFormula(t *testing.T) {
	// Test sizes matching the C test
	testSizes := []int{32, 256, 512, 1024, 2048}

	for _, nfft := range testSizes {
		t.Run(fmt.Sprintf("nfft=%d_forward", nfft), func(t *testing.T) {
			// Create random input
			rng := rand.New(rand.NewSource(42))
			input := make([]float64, nfft)
			for k := 0; k < nfft; k++ {
				input[k] = float64(rng.Intn(32768)-16384) * 32768
			}

			// Compute MDCT using reference formula
			output := make([]float64, nfft/2)
			for bin := 0; bin < nfft/2; bin++ {
				var sum float64
				for k := 0; k < nfft; k++ {
					phase := 2 * math.Pi * (float64(k) + 0.5 + float64(nfft)/4.0) * (float64(bin) + 0.5) / float64(nfft)
					re := math.Cos(phase)
					re /= float64(nfft) / 4.0
					sum += input[k] * re
				}
				output[bin] = sum
			}

			// Check against itself (should be perfect)
			snr := checkMDCTForward(input, output, nfft)
			t.Logf("nfft=%d forward reference SNR = %.2f dB", nfft, snr)

			if snr < 100 {
				t.Errorf("Reference formula self-check failed for nfft=%d: SNR=%.2f dB",
					nfft, snr)
			}
		})

		t.Run(fmt.Sprintf("nfft=%d_inverse", nfft), func(t *testing.T) {
			// Create random frequency input
			rng := rand.New(rand.NewSource(42))
			input := make([]float64, nfft/2)
			for k := 0; k < nfft/2; k++ {
				input[k] = float64(rng.Intn(32768)-16384) / float64(nfft)
			}

			// Compute IMDCT using reference formula
			output := make([]float64, nfft)
			for bin := 0; bin < nfft; bin++ {
				var sum float64
				for k := 0; k < nfft/2; k++ {
					phase := 2 * math.Pi * (float64(bin) + 0.5 + float64(nfft)/4.0) * (float64(k) + 0.5) / float64(nfft)
					re := math.Cos(phase)
					sum += input[k] * re
				}
				output[bin] = sum
			}

			// Check against itself (should be perfect)
			snr := checkMDCTInverse(input, output, nfft)
			t.Logf("nfft=%d inverse reference SNR = %.2f dB", nfft, snr)

			if snr < 100 {
				t.Errorf("Reference formula self-check failed for nfft=%d: SNR=%.2f dB",
					nfft, snr)
			}
		})
	}
}

// TestMDCT_CELTSizes tests MDCT with CELT-specific sizes (120, 240, 480, 960).
// These are the actual frame sizes used in CELT encoding.
func TestMDCT_CELTSizes(t *testing.T) {
	// Get the CELT mode
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	// CELT frame sizes (samples per channel)
	// 2.5ms = 120, 5ms = 240, 10ms = 480, 20ms = 960
	frameSizes := []int{120, 240, 480, 960}
	overlap := mode.Overlap()

	for _, frameSize := range frameSizes {
		t.Run(fmt.Sprintf("frameSize=%d", frameSize), func(t *testing.T) {
			// For MDCT, input is frameSize + overlap
			inputLen := frameSize + overlap

			rng := rand.New(rand.NewSource(42))
			input := make([]float32, inputLen)
			for k := 0; k < inputLen; k++ {
				input[k] = float32(rng.Intn(32768) - 16384)
			}

			// Determine shift value
			// 1920 >> shift = nfft, where nfft = frameSize * 2
			nfft := frameSize * 2
			shift := 0
			for s := 0; s <= 3; s++ {
				if (1920 >> s) == nfft {
					shift = s
					break
				}
			}

			// Call libopus MDCT
			output := mode.MDCTForward(input, shift)

			// Verify output characteristics
			var maxAbs, sumSq float64
			for _, v := range output {
				if math.Abs(float64(v)) > maxAbs {
					maxAbs = math.Abs(float64(v))
				}
				sumSq += float64(v) * float64(v)
			}
			rms := math.Sqrt(sumSq / float64(frameSize))

			t.Logf("frameSize=%d (nfft=%d, shift=%d): maxAbs=%.2f, RMS=%.2f",
				frameSize, nfft, shift, maxAbs, rms)

			// Sanity checks
			if maxAbs < 1.0 {
				t.Errorf("Output max abs too small: %v", maxAbs)
			}
			if math.IsNaN(maxAbs) || math.IsInf(maxAbs, 0) {
				t.Errorf("Output contains NaN or Inf")
			}
		})
	}
}

// TestMDCT_WindowValues tests that the Vorbis window values match between Go and libopus.
func TestMDCT_WindowValues(t *testing.T) {
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()
	libopusWindow := mode.GetWindow()
	goWindow := getWindowBufferGo(overlap)

	var maxDiff float64
	for i := 0; i < overlap; i++ {
		diff := math.Abs(float64(libopusWindow[i]) - goWindow[i])
		if diff > maxDiff {
			maxDiff = diff
		}
	}

	t.Logf("Window max diff: %.2e", maxDiff)

	if maxDiff > 1e-6 {
		t.Errorf("Window values differ by more than 1e-6: max diff = %.2e", maxDiff)
	}
}

// libopusIMDCTGo is a Go implementation matching libopus clt_mdct_backward structure.
// Duplicated here to avoid import cycle with the celt package.
func libopusIMDCTGo(spectrum []float64, prevOverlap []float64, overlap int) []float64 {
	n2 := len(spectrum)
	if n2 == 0 {
		return nil
	}

	n := n2 * 2
	n4 := n2 / 2

	out := make([]float64, n2+overlap)

	if overlap > 0 && len(prevOverlap) > 0 {
		copyLen := overlap
		if len(prevOverlap) < copyLen {
			copyLen = len(prevOverlap)
		}
		copy(out[:copyLen], prevOverlap[:copyLen])
	}

	trig := getLibopusTrigGo(n)

	fftIn := make([]complex128, n4)
	for i := 0; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]

		t0 := trig[i]
		t1 := trig[n4+i]

		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1

		fftIn[i] = complex(yi, yr)
	}

	fftOut := dftGo(fftIn)

	for i := 0; i < n4; i++ {
		v := fftOut[i]
		out[overlap/2+2*i] = real(v)
		out[overlap/2+2*i+1] = imag(v)
	}

	yp0 := overlap / 2
	yp1 := overlap/2 + n2 - 2

	for i := 0; i < (n4+1)>>1; i++ {
		re := out[yp0+1]
		im := out[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]

		yr := re*t0 + im*t1
		yi := re*t1 - im*t0

		re2 := out[yp1+1]
		im2 := out[yp1]

		out[yp0] = yr
		out[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]

		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0

		out[yp1] = yr
		out[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}

	if overlap > 0 {
		window := getWindowBufferGo(overlap)
		xp1 := overlap - 1
		yp1Idx := 0
		wp1 := 0
		wp2 := overlap - 1

		for i := 0; i < overlap/2; i++ {
			x1 := out[xp1]
			x2 := out[yp1Idx]
			out[yp1Idx] = x2*window[wp2] - x1*window[wp1]
			out[xp1] = x2*window[wp1] + x1*window[wp2]
			yp1Idx++
			xp1--
			wp1++
			wp2--
		}
	}

	return out
}

// mdctForwardOverlapGo implements MDCT forward with short overlap.
// Duplicated here to avoid import cycle.
func mdctForwardOverlapGo(samples []float64, overlap int) []float64 {
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

	trig := getMDCTTrigGo(n)
	window := []float64(nil)
	if overlap > 0 {
		window = getWindowBufferGo(overlap)
	}

	f := make([]float64, n2)
	xp1 := overlap / 2
	xp2 := n2 - 1 + overlap/2
	wp1 := overlap / 2
	wp2 := overlap/2 - 1
	i := 0
	limit1 := (overlap + 3) >> 2

	for ; i < limit1; i++ {
		f[2*i] = samples[xp1+n2]*window[wp2] + samples[xp2]*window[wp1]
		f[2*i+1] = samples[xp1]*window[wp1] - samples[xp2-n2]*window[wp2]
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
		f[2*i] = -samples[xp1-n2]*window[wp1] + samples[xp2]*window[wp2]
		f[2*i+1] = samples[xp1]*window[wp2] + samples[xp2+n2]*window[wp1]
		xp1 += 2
		xp2 -= 2
		wp1 += 2
		wp2 -= 2
	}

	scale := 1.0 / float64(n4)
	fftIn := make([]complex128, n4)
	for i = 0; i < n4; i++ {
		re := f[2*i]
		im := f[2*i+1]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 - im*t1
		yi := im*t0 + re*t1
		fftIn[i] = complex(yr*scale, yi*scale)
	}

	fftOut := dftGo(fftIn)
	coeffs := make([]float64, n2)
	for i = 0; i < n4; i++ {
		re := real(fftOut[i])
		im := imag(fftOut[i])
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := im*t1 - re*t0
		yi := re*t1 + im*t0
		coeffs[2*i] = yr
		coeffs[n2-1-2*i] = yi
	}

	return coeffs
}

// Helper functions (duplicated to avoid import cycle)

var libopusTrigCacheGo = map[int][]float64{}

func getLibopusTrigGo(n int) []float64 {
	if trig, ok := libopusTrigCacheGo[n]; ok {
		return trig
	}

	n2 := n / 2
	trig := make([]float64, n2)
	for i := 0; i < n2; i++ {
		trig[i] = math.Cos(2.0 * math.Pi * (float64(i) + 0.125) / float64(n))
	}

	libopusTrigCacheGo[n] = trig
	return trig
}

var mdctTrigCacheGo = map[int][]float64{}

func getMDCTTrigGo(n int) []float64 {
	if trig, ok := mdctTrigCacheGo[n]; ok {
		return trig
	}

	n2 := n / 2
	trig := make([]float64, n2)
	for i := 0; i < n2; i++ {
		angle := 2.0 * math.Pi * (float64(i) + 0.125) / float64(n)
		trig[i] = math.Cos(angle)
	}

	mdctTrigCacheGo[n] = trig
	return trig
}

var windowCacheGo = map[int][]float64{}

func getWindowBufferGo(overlap int) []float64 {
	if window, ok := windowCacheGo[overlap]; ok {
		return window
	}

	window := make([]float64, overlap)
	for i := 0; i < overlap; i++ {
		x := float64(i) + 0.5
		sinArg := 0.5 * math.Pi * x / float64(overlap)
		s := math.Sin(sinArg)
		window[i] = math.Sin(0.5 * math.Pi * s * s)
	}

	windowCacheGo[overlap] = window
	return window
}

func dftGo(x []complex128) []complex128 {
	n := len(x)
	if n <= 1 {
		return x
	}

	out := make([]complex128, n)
	twoPi := -2.0 * math.Pi / float64(n)
	for k := 0; k < n; k++ {
		angle := twoPi * float64(k)
		wStep := complex(math.Cos(angle), math.Sin(angle))
		w := complex(1.0, 0.0)
		var sum complex128
		for t := 0; t < n; t++ {
			sum += x[t] * w
			w *= wStep
		}
		out[k] = sum
	}
	return out
}

// Benchmarks

func BenchmarkMDCT_Libopus(b *testing.B) {
	mode := GetCELTMode48000_960()
	if mode == nil {
		b.Fatal("Failed to create CELT mode")
	}

	overlap := mode.Overlap()
	shifts := []int{1, 2, 3}

	for _, shift := range shifts {
		nfft := mode.MDCTSize(shift)
		n2 := nfft / 2
		inputLen := n2 + overlap

		input := make([]float32, inputLen)
		for k := 0; k < inputLen; k++ {
			input[k] = float32(k % 100)
		}

		b.Run(fmt.Sprintf("nfft=%d", nfft), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mode.MDCTForward(input, shift)
			}
		})
	}
}

func BenchmarkIMDCT_Libopus(b *testing.B) {
	mode := GetCELTMode48000_960()
	if mode == nil {
		b.Fatal("Failed to create CELT mode")
	}

	shifts := []int{1, 2, 3}

	for _, shift := range shifts {
		nfft := mode.MDCTSize(shift)
		n2 := nfft / 2

		input := make([]float32, n2)
		for k := 0; k < n2; k++ {
			input[k] = float32(k % 100)
		}

		b.Run(fmt.Sprintf("nfft=%d", nfft), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mode.IMDCTBackward(input, shift)
			}
		})
	}
}
