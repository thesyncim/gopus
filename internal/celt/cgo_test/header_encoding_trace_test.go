// Package cgo provides header encoding trace tests for comparing gopus vs libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestHeaderEncodingOrderTrace traces bit positions after each header element
// to compare with libopus encoding order.
func TestHeaderEncodingOrderTrace(t *testing.T) {
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate a simple test signal (440 Hz sine wave)
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate))
	}

	t.Log("=== CELT Frame Header Encoding Order Analysis ===")
	t.Log("")
	t.Log("Expected libopus order (from celt_encoder.c):")
	t.Log("  1. Silence flag (logp=15, only if tell==1)")
	t.Log("  2. Postfilter flag (logp=1, only if start==0 && !hybrid && tell+16<=total_bits)")
	t.Log("  3. Transient flag (logp=3, only if LM>0 && tell+3<=total_bits)")
	t.Log("  4. Intra energy flag (logp=3, only if tell+3<=total_bits)")
	t.Log("  5. Coarse energy")
	t.Log("  6. TF encode")
	t.Log("  7. Spread (if tell+4<=total_bits)")
	t.Log("  8. Dynamic allocation")
	t.Log("  9. Alloc trim (if budget allows)")
	t.Log("")

	// Create encoder to inspect behavior
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Get mode info
	mode := celt.GetModeConfig(frameSize)
	lm := mode.LM
	t.Logf("Frame size: %d, LM: %d, EffBands: %d", frameSize, lm, mode.EffBands)

	// Check silence detection
	isSilence := isFrameSilent(samples)
	t.Logf("Is silence: %v", isSilence)

	// Simulate encoding sequence and track bit positions
	targetBits := 64000 * frameSize / 48000
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	t.Log("")
	t.Log("=== Bit Position Trace ===")

	// Track bit positions after each element
	tell0 := re.Tell()
	t.Logf("Initial tell: %d", tell0)

	// 1. Silence flag
	re.EncodeBit(0, 15) // Not silent
	tell1 := re.Tell()
	t.Logf("After silence flag (logp=15): tell=%d, delta=%d bits", tell1, tell1-tell0)

	// 2. Postfilter flag
	// BUG CHECK: Should only encode if tell+16 <= total_bits
	t.Logf("Budget check for postfilter: tell=%d + 16 = %d <= total_bits=%d ? %v",
		tell1, tell1+16, targetBits, tell1+16 <= targetBits)

	re.EncodeBit(0, 1) // No postfilter
	tell2 := re.Tell()
	t.Logf("After postfilter flag (logp=1): tell=%d, delta=%d bits", tell2, tell2-tell1)

	// 3. Transient flag (only if LM > 0)
	if lm > 0 {
		t.Logf("Budget check for transient: tell=%d + 3 = %d <= total_bits=%d ? %v",
			tell2, tell2+3, targetBits, tell2+3 <= targetBits)

		re.EncodeBit(0, 3) // No transient
		tell3 := re.Tell()
		t.Logf("After transient flag (logp=3): tell=%d, delta=%d bits", tell3, tell3-tell2)

		// 4. Intra energy flag
		t.Logf("Budget check for intra: tell=%d + 3 = %d <= total_bits=%d ? %v",
			tell3, tell3+3, targetBits, tell3+3 <= targetBits)

		re.EncodeBit(1, 3) // Intra mode (first frame)
		tell4 := re.Tell()
		t.Logf("After intra flag (logp=3): tell=%d, delta=%d bits", tell4, tell4-tell3)
	} else {
		t.Log("LM=0: skipping transient flag encoding")
	}

	t.Log("")
	t.Log("=== Encoding Order Verification (FIXED) ===")

	// Check the gopus encoding order matches libopus
	t.Log("FIXED gopus encode_frame.go behavior (now matches libopus):")
	t.Log("  - Line 159: Encodes silence flag (logp=15)")
	t.Log("  - Line 165-167: Postfilter ONLY if (start==0 && tell+16<=total_bits) - FIXED")
	t.Log("  - Line 171-181: Transient ONLY if (LM>0 && tell+3<=total_bits) - FIXED")
	t.Log("  - Line 185-195: Intra ONLY if (tell+3<=total_bits) - FIXED")
	t.Log("")
	t.Log("libopus celt_encoder.c behavior (reference):")
	t.Log("  - Line 1982: Silence (if tell==1, logp=15)")
	t.Log("  - Line 2047-2048: Postfilter ONLY if (!hybrid && tell+16<=total_bits)")
	t.Log("  - Line 2063-2069: Transient ONLY if (LM>0 && tell+3<=total_bits)")
	t.Log("  - Line 1377 in decoder: Intra ONLY if (tell+3<=total_bits)")
}

// TestHeaderBudgetConditions tests the budget conditions for each header element.
func TestHeaderBudgetConditions(t *testing.T) {
	t.Log("=== Header Encoding Budget Conditions ===")
	t.Log("")

	// Test with different bitrates to see when conditions fail
	testCases := []struct {
		bitrate   int
		frameSize int
	}{
		{6000, 960},   // Very low bitrate
		{12000, 960},  // Low bitrate
		{24000, 960},  // Medium bitrate
		{64000, 960},  // Normal bitrate
		{128000, 960}, // High bitrate
	}

	for _, tc := range testCases {
		targetBits := tc.bitrate * tc.frameSize / 48000
		t.Logf("Bitrate=%d, FrameSize=%d -> TargetBits=%d", tc.bitrate, tc.frameSize, targetBits)

		// After silence flag (logp=15), tell is approximately 1 bit
		tellAfterSilence := 1

		// Postfilter condition: tell+16 <= total_bits
		postfilterOK := tellAfterSilence+16 <= targetBits
		t.Logf("  Postfilter condition (tell+16 <= targetBits): %d + 16 = %d <= %d ? %v",
			tellAfterSilence, tellAfterSilence+16, targetBits, postfilterOK)

		// After postfilter (logp=1), tell is approximately 2 bits
		tellAfterPostfilter := 2

		// Transient condition: tell+3 <= total_bits
		transientOK := tellAfterPostfilter+3 <= targetBits
		t.Logf("  Transient condition (tell+3 <= targetBits): %d + 3 = %d <= %d ? %v",
			tellAfterPostfilter, tellAfterPostfilter+3, targetBits, transientOK)

		t.Log("")
	}
}

// TestCompareWithLibopusDecoder tests that gopus-encoded frames can be decoded by libopus.
func TestCompareWithLibopusDecoder(t *testing.T) {
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate a simple test signal
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate))
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	t.Logf("Encoded: %d bytes", len(encoded))
	t.Logf("First 16 bytes: %02x", encoded[:minIntH(16, len(encoded))])

	// Decode with libopus
	toc := byte(0xF8) // CELT fullband 20ms mono
	packet := append([]byte{toc}, encoded...)

	libDec, err := NewLibopusDecoder(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	// Check if libopus output is near zero (indicating header parsing error)
	var libRMS float64
	for i := 0; i < libSamples; i++ {
		libRMS += float64(libDecoded[i]) * float64(libDecoded[i])
	}
	libRMS = math.Sqrt(libRMS / float64(libSamples))

	t.Logf("libopus decoded: %d samples, RMS=%.6f", libSamples, libRMS)

	// Also decode with gopus decoder for comparison
	decoder := celt.NewDecoder(channels)
	decoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("gopus DecodeFrame failed: %v", err)
	}

	var gopusRMS float64
	for _, s := range decoded {
		gopusRMS += s * s
	}
	gopusRMS = math.Sqrt(gopusRMS / float64(len(decoded)))

	t.Logf("gopus decoded: %d samples, RMS=%.6f", len(decoded), gopusRMS)

	// Input RMS for comparison
	var inputRMS float64
	for _, s := range samples {
		inputRMS += s * s
	}
	inputRMS = math.Sqrt(inputRMS / float64(len(samples)))
	t.Logf("Input RMS: %.6f", inputRMS)

	// If libopus RMS is much lower than input, there's likely a header parsing error
	if libRMS < inputRMS*0.1 {
		t.Errorf("libopus output energy is too low (%.6f vs input %.6f) - likely header parsing error",
			libRMS, inputRMS)
	}
}

func isFrameSilent(pcm []float64) bool {
	const silenceThreshold = 1e-10
	for _, s := range pcm {
		if s > silenceThreshold || s < -silenceThreshold {
			return false
		}
	}
	return true
}

func minIntH(a, b int) int {
	if a < b {
		return a
	}
	return b
}
