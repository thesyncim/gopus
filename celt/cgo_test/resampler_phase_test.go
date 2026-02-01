//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides tests to compare resampler phase/timing between gopus and libopus.
package cgo

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/silk"
)

// TestResamplerPhaseComparison compares resampler phase between gopus and libopus
func TestResamplerPhaseComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 1)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet: %d bytes, bandwidth=%d, frameSize=%d", len(pkt), toc.Bandwidth, toc.FrameSize)

	// Decode with fresh decoders
	libDec, err := NewLibopusDecoder(48000, 1)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))

	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	goPcm, decErr := decodeFloat32(goDec, pkt)
	if decErr != nil {
		t.Fatalf("gopus decode failed: %v", decErr)
	}

	t.Logf("Samples: gopus=%d, libopus=%d", len(goPcm), libSamples)

	// Find first non-zero sample in each
	goFirstNonZero := findFirstNonZero(goPcm)
	libFirstNonZero := findFirstNonZeroF32(libPcm[:libSamples])

	t.Logf("First non-zero: gopus=%d, libopus=%d, diff=%d",
		goFirstNonZero, libFirstNonZero, goFirstNonZero-libFirstNonZero)

	// Find first significant transition (value change > threshold)
	goFirstTransition := findFirstTransition(goPcm, 0.00001)
	libFirstTransition := findFirstTransitionF32(libPcm[:libSamples], 0.00001)

	t.Logf("First transition: gopus=%d, libopus=%d, diff=%d",
		goFirstTransition, libFirstTransition, goFirstTransition-libFirstTransition)

	// Cross-correlate small window to find precise offset
	windowSize := 100
	maxLag := 10
	bestLag, bestCorr := crossCorrelateWindow(goPcm, libPcm[:libSamples], 0, windowSize, maxLag)
	t.Logf("Cross-correlation [0:%d]: best lag=%d, correlation=%.6f", windowSize, bestLag, bestCorr)

	// Check at different positions
	positions := []int{0, 100, 500, 1000, 2000}
	for _, pos := range positions {
		if pos+windowSize > minInt(len(goPcm), libSamples) {
			continue
		}
		lag, corr := crossCorrelateWindow(goPcm, libPcm[:libSamples], pos, windowSize, maxLag)
		t.Logf("Cross-correlation [%d:%d]: best lag=%d, correlation=%.6f",
			pos, pos+windowSize, lag, corr)
	}

	// Show samples around the first transition
	t.Log("\nSamples around first activity:")
	start := maxIntLocal(0, minInt(goFirstNonZero, libFirstNonZero)-5)
	end := minInt(minInt(len(goPcm), libSamples), start+30)
	t.Log("Index\tgopus\t\tlibopus\t\tdiff")
	for i := start; i < end; i++ {
		diff := goPcm[i] - libPcm[i]
		marker := ""
		if math.Abs(float64(diff)) > 0.00001 {
			marker = " *"
		}
		t.Logf("%d\t%.6f\t%.6f\t%.6f%s", i, goPcm[i], libPcm[i], diff, marker)
	}
}

// TestResamplerImpulseResponse tests resampler with known impulse signals
func TestResamplerImpulseResponse(t *testing.T) {
	// Test our resampler with a simple impulse
	testCases := []struct {
		name       string
		inputRate  int
		outputRate int
		impulsePos int
		inputLen   int
	}{
		{"8kHz->48kHz impulse@0", 8000, 48000, 0, 80},
		{"8kHz->48kHz impulse@4", 8000, 48000, 4, 80},
		{"8kHz->48kHz impulse@10", 8000, 48000, 10, 80},
		{"12kHz->48kHz impulse@4", 12000, 48000, 4, 120},
		{"16kHz->48kHz impulse@4", 16000, 48000, 4, 160},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create input with single impulse
			input := make([]float32, tc.inputLen)
			input[tc.impulsePos] = 1.0

			// Resample
			resampler := silk.NewLibopusResampler(tc.inputRate, tc.outputRate)
			output := resampler.Process(input)

			ratio := tc.outputRate / tc.inputRate
			expectedLen := tc.inputLen * ratio
			t.Logf("Input: %d samples, Output: %d samples (expected %d)", tc.inputLen, len(output), expectedLen)

			// Find peak in output
			peakIdx, peakVal := findPeak(output)
			expectedPeakPos := tc.impulsePos * ratio

			t.Logf("Impulse at input[%d] -> peak at output[%d] (expected ~%d), value=%.6f",
				tc.impulsePos, peakIdx, expectedPeakPos, peakVal)
			t.Logf("Delay: %d output samples = %.2f input samples",
				peakIdx-expectedPeakPos, float64(peakIdx-expectedPeakPos)/float64(ratio))

			// Show samples around the peak
			start := maxIntLocal(0, peakIdx-10)
			end := minInt(len(output), peakIdx+10)
			t.Log("Output around peak:")
			for i := start; i < end; i++ {
				marker := ""
				if i == peakIdx {
					marker = " <-- peak"
				}
				if i == expectedPeakPos {
					marker += " <-- expected"
				}
				t.Logf("  [%3d] %.6f%s", i, output[i], marker)
			}
		})
	}
}

// TestResamplerStepResponse tests resampler with step function
func TestResamplerStepResponse(t *testing.T) {
	// Create input with step at sample 10
	inputLen := 80
	stepPos := 10
	input := make([]float32, inputLen)
	for i := stepPos; i < inputLen; i++ {
		input[i] = 1.0
	}

	resampler := silk.NewLibopusResampler(8000, 48000)
	output := resampler.Process(input)

	t.Logf("Step at input[%d], output length=%d", stepPos, len(output))

	// Find where output crosses 0.5 (50% point of step)
	crossIdx := -1
	for i := 1; i < len(output); i++ {
		if output[i-1] < 0.5 && output[i] >= 0.5 {
			crossIdx = i
			break
		}
	}

	expectedCross := stepPos * 6 // 6x upsampling
	if crossIdx >= 0 {
		t.Logf("50%% crossing at output[%d] (expected ~%d), delay=%d samples",
			crossIdx, expectedCross, crossIdx-expectedCross)
	}

	// Show step response
	start := maxIntLocal(0, expectedCross-20)
	end := minInt(len(output), expectedCross+30)
	t.Log("Step response:")
	for i := start; i < end; i++ {
		marker := ""
		if i == crossIdx {
			marker = " <-- 50% crossing"
		}
		if i == expectedCross {
			marker += " <-- expected"
		}
		t.Logf("  [%3d] %.6f%s", i, output[i], marker)
	}
}

// TestInvRatioQ16Calculation verifies our invRatio_Q16 calculation matches libopus
func TestInvRatioQ16Calculation(t *testing.T) {
	testCases := []struct {
		fsIn  int
		fsOut int
	}{
		{8000, 48000},
		{12000, 48000},
		{16000, 48000},
		{8000, 24000},
		{12000, 24000},
		{16000, 24000},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%d->%d", tc.fsIn, tc.fsOut), func(t *testing.T) {
			// Our calculation
			up2x := 1 // For upsampling with IIR_FIR
			invRatio := int32((tc.fsIn << (14 + up2x)) / tc.fsOut)
			invRatioQ16 := invRatio << 2

			// Rounding up (matching libopus)
			for smulww(invRatioQ16, int32(tc.fsOut)) < int32(tc.fsIn<<up2x) {
				invRatioQ16++
			}

			// Calculate actual ratio
			actualRatio := float64(invRatioQ16) / 65536.0
			expectedRatio := float64(tc.fsIn*2) / float64(tc.fsOut) // *2 because of 2x upsampling

			t.Logf("invRatioQ16 = %d (0x%08x)", invRatioQ16, invRatioQ16)
			t.Logf("Actual ratio = %.10f, Expected = %.10f, Error = %.2e",
				actualRatio, expectedRatio, actualRatio-expectedRatio)

			// Calculate samples per output
			samplesPerOutput := 65536.0 / float64(invRatioQ16)
			t.Logf("Output samples per 2x-upsampled input sample: %.6f", samplesPerOutput)
		})
	}
}

// TestFirstFrameBuffering tests the delay buffer behavior on first frame
func TestFirstFrameBuffering(t *testing.T) {
	// Test with different frame sizes to see if delay buffer affects timing
	frameSizes := []int{80, 160, 320, 480} // 10ms, 20ms, 40ms, 60ms at 8kHz

	for _, frameSize := range frameSizes {
		t.Run(fmt.Sprintf("frameSize=%d", frameSize), func(t *testing.T) {
			// Create ramp input
			input := make([]float32, frameSize)
			for i := range input {
				input[i] = float32(i) / float32(frameSize)
			}

			resampler := silk.NewLibopusResampler(8000, 48000)
			output := resampler.Process(input)

			t.Logf("Input: %d samples, Output: %d samples", frameSize, len(output))

			// Check first few output samples
			t.Log("First 20 output samples:")
			for i := 0; i < minInt(20, len(output)); i++ {
				// Expected value based on linear interpolation of ramp
				// (accounting for filter delay)
				t.Logf("  [%2d] %.6f", i, output[i])
			}

			// Find where output becomes significant
			firstSig := -1
			for i, v := range output {
				if v > 0.001 {
					firstSig = i
					break
				}
			}
			t.Logf("First significant sample (>0.001): %d", firstSig)
		})
	}
}

// TestSMidBufferEffect tests the effect of sMid buffer on timing
func TestSMidBufferEffect(t *testing.T) {
	// Simulate what happens with sMid buffering
	t.Log("Testing sMid buffer effect on timing...")

	// Original samples (simulating native SILK output)
	nativeSamples := make([]float32, 80)
	for i := range nativeSamples {
		nativeSamples[i] = float32(i + 1) // 1, 2, 3, ...
	}

	// Without sMid buffering (direct resample)
	resampler1 := silk.NewLibopusResampler(8000, 48000)
	outputDirect := resampler1.Process(nativeSamples)

	// With sMid buffering (like libopus)
	// sMid starts as [0, 0]
	sMid := [2]float32{0, 0}
	n := len(nativeSamples)
	resamplerInput := make([]float32, n)
	resamplerInput[0] = sMid[1] // 0
	copy(resamplerInput[1:], nativeSamples[:n-1])
	// Update sMid for next frame
	sMid[0] = nativeSamples[n-2]
	sMid[1] = nativeSamples[n-1]

	resampler2 := silk.NewLibopusResampler(8000, 48000)
	outputSMid := resampler2.Process(resamplerInput)

	t.Log("Comparing direct vs sMid-buffered output:")
	t.Log("Index\tDirect\t\tsMid\t\tDiff")
	for i := 0; i < minInt(30, minInt(len(outputDirect), len(outputSMid))); i++ {
		diff := outputDirect[i] - outputSMid[i]
		t.Logf("%d\t%.6f\t%.6f\t%.6f", i, outputDirect[i], outputSMid[i], diff)
	}

	// The sMid version should be delayed by approximately 1 native sample = 6 output samples
	t.Log("\nsMid buffering should delay output by ~6 samples (1 native sample at 6x)")
}

// Helper functions

func findFirstNonZero(samples []float32) int {
	for i, v := range samples {
		if v != 0 {
			return i
		}
	}
	return -1
}

func findFirstNonZeroF32(samples []float32) int {
	for i, v := range samples {
		if v != 0 {
			return i
		}
	}
	return -1
}

func findFirstTransition(samples []float32, threshold float64) int {
	for i := 1; i < len(samples); i++ {
		if math.Abs(float64(samples[i]-samples[i-1])) > threshold {
			return i
		}
	}
	return -1
}

func findFirstTransitionF32(samples []float32, threshold float64) int {
	for i := 1; i < len(samples); i++ {
		if math.Abs(float64(samples[i]-samples[i-1])) > threshold {
			return i
		}
	}
	return -1
}

func crossCorrelateWindow(a, b []float32, start, windowSize, maxLag int) (bestLag int, bestCorr float64) {
	bestCorr = -math.MaxFloat64

	for lag := -maxLag; lag <= maxLag; lag++ {
		var sum, normA, normB float64

		for i := 0; i < windowSize; i++ {
			idxA := start + i
			idxB := start + i + lag

			if idxA < 0 || idxA >= len(a) || idxB < 0 || idxB >= len(b) {
				continue
			}

			va := float64(a[idxA])
			vb := float64(b[idxB])

			sum += va * vb
			normA += va * va
			normB += vb * vb
		}

		if normA > 0 && normB > 0 {
			corr := sum / math.Sqrt(normA*normB)
			if corr > bestCorr {
				bestCorr = corr
				bestLag = lag
			}
		}
	}

	return
}

func findPeak(samples []float32) (int, float32) {
	peakIdx := 0
	peakVal := float32(0)
	for i, v := range samples {
		if math.Abs(float64(v)) > math.Abs(float64(peakVal)) {
			peakIdx = i
			peakVal = v
		}
	}
	return peakIdx, peakVal
}

// smulww matches libopus: (a * b) >> 16
func smulww(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 16)
}

// maxIntLocal is a local version to avoid redeclaration
func maxIntLocal(a, b int) int {
	if a > b {
		return a
	}
	return b
}
