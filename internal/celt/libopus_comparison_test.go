package celt

import (
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestCELTDivergenceDiagnosis analyzes the root cause of Q=-100 in CELT decoding.
//
// Based on analysis from bitstream_comparison_test.go, this test documents the
// specific divergence finding and generates a diagnosis with hypothesis about
// the root cause.
//
// Key findings from investigation:
// 1. Decoded output is all zeros while reference has substantial audio content
// 2. No trace output, indicating decode pipeline never runs
// 3. Silence flag (DecodeBit(15)) returns 1 for all frames
// 4. This causes decodeSilenceFrame() to be called instead of actual decoding
func TestCELTDivergenceDiagnosis(t *testing.T) {
	t.Logf("=== Divergence Diagnosis ===")
	t.Logf("")

	// Observation 1: Output is all zeros
	t.Logf("OBSERVATION 1: Decoder output is all zeros")
	t.Logf("  - For non-silent reference frames (energy/sample > 1e6)")
	t.Logf("  - Energy ratio: 0%% (decoded has no energy)")
	t.Logf("  - Mean absolute difference: ~1000 (matches reference magnitude)")
	t.Logf("")

	// Observation 2: No trace output
	t.Logf("OBSERVATION 2: Tracer captures no decode events")
	t.Logf("  - TraceHeader() never called")
	t.Logf("  - TraceEnergy() never called")
	t.Logf("  - TracePVQ() never called")
	t.Logf("  - This means decode pipeline is bypassed")
	t.Logf("")

	// Observation 3: Silence flag analysis
	t.Logf("OBSERVATION 3: Silence flag incorrectly returns 1")
	t.Logf("  - decodeSilenceFlag() uses DecodeBit(15)")
	t.Logf("  - logp=15 means P(silence) = 1/32768 (very unlikely)")
	t.Logf("  - Yet silence flag returns 1 for all tested frames")
	t.Logf("")

	// Test the DecodeBit implementation
	t.Logf("=== DecodeBit Analysis ===")

	// Create a test buffer with known content
	testData := []byte{0xCF, 0xC5, 0x88, 0x30, 0x00, 0x00, 0x00, 0x00}
	rd := &rangecoding.Decoder{}
	rd.Init(testData)

	t.Logf("Test data: 0x%02X 0x%02X 0x%02X 0x%02X", testData[0], testData[1], testData[2], testData[3])
	t.Logf("After Init: val=0x%08X, rng=0x%08X", rd.Val(), rd.Range())

	// Analyze DecodeBit(15) behavior
	rng := rd.Range()
	val := rd.Val()
	r := rng >> 15 // Size of the "1" region

	t.Logf("")
	t.Logf("DecodeBit(15) analysis:")
	t.Logf("  rng = 0x%08X (%d)", rng, rng)
	t.Logf("  val = 0x%08X (%d)", val, val)
	t.Logf("  r = rng >> 15 = 0x%08X (%d)", r, r)
	t.Logf("")
	t.Logf("Current implementation:")
	t.Logf("  Condition: val >= r --> %v >= %v --> %v", val, r, val >= r)
	t.Logf("  Returns: %d (1 = silence)", boolToInt(val >= r))
	t.Logf("")

	// Expected behavior per RFC 6716
	t.Logf("Expected behavior (per RFC 6716 Section 4.1):")
	t.Logf("  P(0) = 1 - 1/2^15 = 32767/32768 (almost always 0)")
	t.Logf("  P(1) = 1/2^15 = 1/32768 (very rare)")
	t.Logf("  threshold = rng - r = 0x%08X", rng-r)
	t.Logf("  Expected: val < threshold --> bit = 0 (non-silence)")
	t.Logf("")

	// Hypothesis
	t.Logf("=== HYPOTHESIS ===")
	t.Logf("")
	t.Logf("ROOT CAUSE: DecodeBit() logic is inverted")
	t.Logf("")
	t.Logf("Current logic (WRONG):")
	t.Logf("  if val >= r { return 1 }  // val is compared against r directly")
	t.Logf("")
	t.Logf("Expected logic (CORRECT):")
	t.Logf("  threshold = rng - r  // bottom of '1' probability region")
	t.Logf("  if val >= threshold { return 1 }  // '1' is at TOP of range")
	t.Logf("  else { return 0 }")
	t.Logf("")
	t.Logf("The range coder divides [0, rng) into two regions:")
	t.Logf("  [0, rng-r) = probability region for 0 (32767/32768 of range)")
	t.Logf("  [rng-r, rng) = probability region for 1 (1/32768 of range)")
	t.Logf("")
	t.Logf("Current code checks 'val >= r' which is almost always true")
	t.Logf("because val (normalized to ~0x181D3BE7) >> r (~0x10000).")
	t.Logf("")
	t.Logf("This causes EVERY frame to be treated as silence, producing zeros.")
	t.Logf("")

	// Evidence
	t.Logf("=== EVIDENCE ===")
	t.Logf("")
	t.Logf("1. All decoded output is zeros (matches silence frame behavior)")
	t.Logf("2. Tracer never fires (silence path bypasses decode pipeline)")
	t.Logf("3. DecodeBit(15) returns 1 even for high-energy reference frames")
	t.Logf("4. The math: val=0x%08X >> r=0x%08X, so val >= r is always true", val, r)
	t.Logf("")

	// Recommended fix
	t.Logf("=== RECOMMENDED FIX ===")
	t.Logf("")
	t.Logf("In rangecoding/decoder.go DecodeBit():")
	t.Logf("")
	t.Logf("Current:")
	t.Logf("  func (d *Decoder) DecodeBit(logp uint) int {")
	t.Logf("      r := d.rng >> logp")
	t.Logf("      if d.val >= r {")
	t.Logf("          // Bit is 1")
	t.Logf("          ...")
	t.Logf("      }")
	t.Logf("  }")
	t.Logf("")
	t.Logf("Should be:")
	t.Logf("  func (d *Decoder) DecodeBit(logp uint) int {")
	t.Logf("      r := d.rng >> logp")
	t.Logf("      threshold := d.rng - r  // '1' region is at TOP of range")
	t.Logf("      if d.val >= threshold {")
	t.Logf("          // Bit is 1 (rare case)")
	t.Logf("          d.val -= threshold")
	t.Logf("          d.rng = r")
	t.Logf("          ...")
	t.Logf("      } else {")
	t.Logf("          // Bit is 0 (common case)")
	t.Logf("          d.rng = threshold")
	t.Logf("          ...")
	t.Logf("      }")
	t.Logf("  }")
	t.Logf("")

	// Summary
	t.Logf("=== SUMMARY ===")
	t.Logf("")
	t.Logf("Divergence point: Sample 0 (100%% of frames diverge immediately)")
	t.Logf("Pattern: All zeros vs substantial audio content")
	t.Logf("Root cause: DecodeBit() comparison logic inverted")
	t.Logf("Impact: Every CELT frame treated as silence")
	t.Logf("Fix: Correct the threshold comparison in DecodeBit()")
	t.Logf("")
	t.Logf("Next step: Fix DecodeBit() in internal/rangecoding/decoder.go")
}

// TestDecodeBitBehavior tests the range decoder DecodeBit function
// to verify it matches the expected behavior per RFC 6716.
func TestDecodeBitBehavior(t *testing.T) {
	// Test case: very low probability bit (logp=15 means P(1) = 1/32768)
	// With typical initialized state, bit should almost always be 0

	// Create test cases with various starting bytes
	testCases := []struct {
		name     string
		data     []byte
		expected int // expected bit value (0 for most cases with logp=15)
	}{
		// These bytes should produce val values that are in the "0" region
		// (which is 32767/32768 of the total range)
		{"typical_audio_1", []byte{0xCF, 0xC5, 0x88, 0x30}, -1}, // -1 means check current behavior
		{"typical_audio_2", []byte{0x12, 0x34, 0x56, 0x78}, -1},
		{"low_value", []byte{0x00, 0x00, 0x00, 0x01}, -1},
		{"mid_value", []byte{0x7F, 0xFF, 0xFF, 0xFF}, -1},
		{"high_value", []byte{0xFF, 0xFF, 0xFF, 0xFF}, -1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rd := &rangecoding.Decoder{}
			rd.Init(tc.data)

			rng := rd.Range()
			val := rd.Val()
			r := rng >> 15

			// Check what the current implementation returns
			bit := rd.DecodeBit(15)

			// The expected behavior: with logp=15, most values should give 0
			// Only values in the top 1/32768 of the range should give 1
			threshold := rng - r
			expectedCorrect := 0
			if val >= threshold {
				expectedCorrect = 1
			}

			t.Logf("Data: %v", tc.data)
			t.Logf("  val=0x%08X, rng=0x%08X, r=0x%08X, threshold=0x%08X", val, rng, r, threshold)
			t.Logf("  Current result: %d", bit)
			t.Logf("  Expected (correct): %d", expectedCorrect)
			t.Logf("  val >= r: %v (current comparison)", val >= r)
			t.Logf("  val >= threshold: %v (correct comparison)", val >= threshold)

			if bit != expectedCorrect {
				t.Logf("  ** MISMATCH: Current implementation returns %d but should be %d **", bit, expectedCorrect)
			}
		})
	}
}

// TestSilenceFlagDetection tests the silence flag detection logic.
// The silence flag should almost never be true for real audio content.
func TestSilenceFlagDetection(t *testing.T) {
	// Create a decoder and check silence flag behavior
	decoder := NewDecoder(2) // stereo

	// Create some test frame data (typical CELT frame)
	// This data represents a non-silence frame
	testFrameData := []byte{
		0xCF, 0xC5, 0x88, 0x30, 0x45, 0x67, 0x89, 0xAB,
		0xCD, 0xEF, 0x01, 0x23, 0x45, 0x67, 0x89, 0xAB,
	}

	// Initialize range decoder
	rd := &rangecoding.Decoder{}
	rd.Init(testFrameData)
	decoder.SetRangeDecoder(rd)

	// Check silence flag
	silence := decoder.decodeSilenceFlag()

	t.Logf("Test frame data (first 8 bytes): %v", testFrameData[:8])
	t.Logf("Silence flag detected: %v", silence)

	if silence {
		t.Logf("WARNING: Silence detected for what should be audio content")
		t.Logf("This confirms the DecodeBit(15) bug hypothesis")
	} else {
		t.Logf("Silence not detected - this is expected for audio content")
	}

	// The current broken implementation will return silence=true
	// After fix, it should return silence=false for typical audio
}
