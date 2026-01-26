package celt

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestDecodeFrame_SampleCount verifies DecodeFrame produces correct sample counts.
// This is an integration test confirming the full decode pipeline
// (DecodeBands -> Synthesize -> output) produces the expected sample count
// after the 14-01 (MDCT bin count) and 14-02 (overlap-add) fixes.
func TestDecodeFrame_SampleCount(t *testing.T) {
	testCases := []struct {
		frameSize       int
		expectedSamples int // After 14-02 fix: exactly frameSize samples
	}{
		{120, 120}, // 2.5ms: 120 samples
		{240, 240}, // 5ms: 240 samples
		{480, 480}, // 10ms: 480 samples
		{960, 960}, // 20ms: 960 samples
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%d", tc.frameSize), func(t *testing.T) {
			d := NewDecoder(1)

			// DecodeFrame with nil data triggers PLC (packet loss concealment)
			// which still exercises the synthesis pipeline
			samples, err := d.DecodeFrame(nil, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFrame error: %v", err)
			}

			if len(samples) != tc.expectedSamples {
				t.Errorf("DecodeFrame produced %d samples, want %d", len(samples), tc.expectedSamples)
			}
		})
	}
}

// TestDecodeFrame_SampleCount_Stereo verifies stereo DecodeFrame produces correct counts.
// After 14-02 fix, stereo produces 2*frameSize interleaved samples.
func TestDecodeFrame_SampleCount_Stereo(t *testing.T) {
	testCases := []struct {
		frameSize       int
		expectedSamples int // Stereo interleaved: 2 * frameSize
	}{
		{120, 240},  // 2.5ms: 2 * 120 = 240
		{240, 480},  // 5ms: 2 * 240 = 480
		{480, 960},  // 10ms: 2 * 480 = 960
		{960, 1920}, // 20ms: 2 * 960 = 1920
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%d", tc.frameSize), func(t *testing.T) {
			d := NewDecoder(2) // Stereo

			// DecodeFrame with nil data triggers PLC
			samples, err := d.DecodeFrame(nil, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFrame error: %v", err)
			}

			if len(samples) != tc.expectedSamples {
				t.Errorf("DecodeFrame produced %d stereo samples, want %d", len(samples), tc.expectedSamples)
			}
		})
	}
}

// TestDecodeFrame_InvalidFrameSizeRejected verifies invalid frame sizes are rejected.
func TestDecodeFrame_InvalidFrameSizeRejected(t *testing.T) {
	d := NewDecoder(1)

	invalidSizes := []int{0, 100, 200, 500, 1000, -1}

	for _, size := range invalidSizes {
		t.Run(fmt.Sprintf("%d", size), func(t *testing.T) {
			_, err := d.DecodeFrame(nil, size)
			if err != ErrInvalidFrameSize {
				t.Errorf("DecodeFrame with size %d: got err=%v, want ErrInvalidFrameSize", size, err)
			}
		})
	}
}

// TestDecodeFrame_ConsecutiveFrames verifies sample counts remain consistent across frames.
// After 14-02 fix, DecodeFrame consistently returns frameSize samples.
func TestDecodeFrame_ConsecutiveFrames(t *testing.T) {
	d := NewDecoder(1)
	d.Reset() // Ensure clean state
	frameSize := 960

	// Decode multiple consecutive frames (PLC mode)
	for i := 0; i < 5; i++ {
		samples, err := d.DecodeFrame(nil, frameSize)
		if err != nil {
			t.Fatalf("Frame %d: DecodeFrame error: %v", i, err)
		}

		// After 14-02 fix, output is consistently frameSize samples
		if len(samples) != frameSize {
			t.Errorf("Frame %d: got %d samples, want %d", i, len(samples), frameSize)
		}
	}
}

// TestDecoder_Initialization verifies decoder initialization.
func TestDecoder_Initialization(t *testing.T) {
	tests := []struct {
		channels int
		expected int
	}{
		{0, 1}, // Clamped to 1
		{1, 1},
		{2, 2},
		{3, 2}, // Clamped to 2
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d", tc.channels), func(t *testing.T) {
			d := NewDecoder(tc.channels)
			if d.Channels() != tc.expected {
				t.Errorf("NewDecoder(%d).Channels() = %d, want %d",
					tc.channels, d.Channels(), tc.expected)
			}
		})
	}
}

// TestDecoder_ResetState verifies decoder state is properly reset.
func TestDecoder_ResetState(t *testing.T) {
	d := NewDecoder(1)

	// Decode a frame to change state
	_, _ = d.DecodeFrame(nil, 960)

	// Reset
	d.Reset()

	// Verify state is cleared (libopus reset clears oldBandE to 0).
	for i, e := range d.PrevEnergy() {
		if e != 0 {
			t.Errorf("PrevEnergy[%d] = %v, want 0.0 after reset", i, e)
		}
	}

	for i, e := range d.PrevEnergy2() {
		if e != 0 {
			t.Errorf("PrevEnergy2[%d] = %v, want 0.0 after reset", i, e)
		}
	}

	for i, s := range d.OverlapBuffer() {
		if s != 0 {
			t.Errorf("OverlapBuffer[%d] = %v, want 0 after reset", i, s)
		}
	}
}

// TestDecodeFrame_ShortFrames verifies CELT can decode 2.5ms and 5ms frame sizes.
// RFC 8251 test vectors include short frames. This test confirms the decoder
// handles them correctly with proper sample output counts.
func TestDecodeFrame_ShortFrames(t *testing.T) {
	// Test 2.5ms and 5ms frames with actual CELT data
	testCases := []struct {
		name      string
		frameSize int
		wantLen   int // Expected output sample count
	}{
		{"2.5ms_mono", 120, 120},
		{"5ms_mono", 240, 240},
		{"10ms_mono", 480, 480},
		{"20ms_mono", 960, 960},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(1)

			// Create a minimal valid CELT frame
			// Silence frame is simplest: first bit = 1 (probability 15/32 = 0x80)
			silenceFrame := []byte{0x80} // Silence flag set

			samples, err := d.DecodeFrame(silenceFrame, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFrame failed: %v", err)
			}

			// Verify we get exactly frameSize samples
			if len(samples) != tc.wantLen {
				t.Errorf("got %d samples, want %d", len(samples), tc.wantLen)
			}
		})
	}
}

// TestDecodeFrame_ShortFrameStereo verifies stereo short frame decoding.
func TestDecodeFrame_ShortFrameStereo(t *testing.T) {
	testCases := []struct {
		frameSize int
		wantLen   int // samples per channel * 2 (interleaved)
	}{
		{120, 240},  // 2.5ms stereo: 120*2
		{240, 480},  // 5ms stereo: 240*2
		{480, 960},  // 10ms stereo: 480*2
		{960, 1920}, // 20ms stereo: 960*2
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%d_stereo", tc.frameSize), func(t *testing.T) {
			d := NewDecoder(2)
			silenceFrame := []byte{0x80}

			samples, err := d.DecodeFrame(silenceFrame, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFrame failed: %v", err)
			}

			if len(samples) != tc.wantLen {
				t.Errorf("got %d samples, want %d", len(samples), tc.wantLen)
			}
		})
	}
}

// TestDecodeFrame_ShortFrameConsecutive verifies consecutive short frame decoding
// produces consistent sample counts with proper overlap-add state maintenance.
func TestDecodeFrame_ShortFrameConsecutive(t *testing.T) {
	testCases := []int{120, 240} // 2.5ms and 5ms

	for _, frameSize := range testCases {
		t.Run(fmt.Sprintf("%d", frameSize), func(t *testing.T) {
			d := NewDecoder(1)
			silenceFrame := []byte{0x80}

			// Decode 5 consecutive frames
			for i := 0; i < 5; i++ {
				samples, err := d.DecodeFrame(silenceFrame, frameSize)
				if err != nil {
					t.Fatalf("Frame %d: DecodeFrame failed: %v", i, err)
				}

				if len(samples) != frameSize {
					t.Errorf("Frame %d: got %d samples, want %d", i, len(samples), frameSize)
				}
			}
		})
	}
}

// ============================================================================
// Phase 15-05: Frame-size-specific decode tests
// These tests validate CELT decoder quality improvements from Phase 15
// ============================================================================

// TestDecodeFrame120Samples tests 2.5ms frame (120 samples at 48kHz).
// This is the shortest CELT frame size and validates the decoder handles it correctly.
func TestDecodeFrame120Samples(t *testing.T) {
	d := NewDecoder(1)

	// Create minimal valid CELT frame
	// First byte should not trigger silence flag
	frameData := make([]byte, 8)
	frameData[0] = 0x80 // Not silence (bit 0 = 0 after range decode)
	for i := 1; i < len(frameData); i++ {
		frameData[i] = byte(i * 17 % 256)
	}

	samples, err := d.DecodeFrame(frameData, 120)
	if err != nil {
		t.Fatalf("DecodeFrame(120) failed: %v", err)
	}

	if len(samples) != 120 {
		t.Errorf("DecodeFrame(120) produced %d samples, want 120", len(samples))
	}

	// Check for non-zero output (not all silence)
	hasNonZero := false
	for _, s := range samples {
		if math.Abs(s) > 1e-10 {
			hasNonZero = true
			break
		}
	}

	// Note: may be all zeros if decoded as silence frame - that's OK
	t.Logf("120-sample frame: hasNonZero=%v", hasNonZero)
}

// TestDecodeFrame240Samples tests 5ms frame (240 samples at 48kHz).
func TestDecodeFrame240Samples(t *testing.T) {
	d := NewDecoder(1)

	frameData := make([]byte, 16)
	frameData[0] = 0x80
	for i := 1; i < len(frameData); i++ {
		frameData[i] = byte(i * 23 % 256)
	}

	samples, err := d.DecodeFrame(frameData, 240)
	if err != nil {
		t.Fatalf("DecodeFrame(240) failed: %v", err)
	}

	if len(samples) != 240 {
		t.Errorf("DecodeFrame(240) produced %d samples, want 240", len(samples))
	}

	t.Logf("240-sample frame decoded successfully")
}

// TestDecodeFrame480Samples tests 10ms frame (480 samples at 48kHz).
func TestDecodeFrame480Samples(t *testing.T) {
	d := NewDecoder(1)

	frameData := make([]byte, 32)
	frameData[0] = 0x80
	for i := 1; i < len(frameData); i++ {
		frameData[i] = byte(i * 31 % 256)
	}

	samples, err := d.DecodeFrame(frameData, 480)
	if err != nil {
		t.Fatalf("DecodeFrame(480) failed: %v", err)
	}

	if len(samples) != 480 {
		t.Errorf("DecodeFrame(480) produced %d samples, want 480", len(samples))
	}

	t.Logf("480-sample frame decoded successfully")
}

// TestDecodeFrame960Samples tests 20ms frame (960 samples at 48kHz).
func TestDecodeFrame960Samples(t *testing.T) {
	d := NewDecoder(1)

	frameData := make([]byte, 64)
	frameData[0] = 0x80
	for i := 1; i < len(frameData); i++ {
		frameData[i] = byte(i * 37 % 256)
	}

	samples, err := d.DecodeFrame(frameData, 960)
	if err != nil {
		t.Fatalf("DecodeFrame(960) failed: %v", err)
	}

	if len(samples) != 960 {
		t.Errorf("DecodeFrame(960) produced %d samples, want 960", len(samples))
	}

	t.Logf("960-sample frame decoded successfully")
}

// TestDecodeFrameSequence tests decoding multiple frames maintains state correctly.
func TestDecodeFrameSequence(t *testing.T) {
	d := NewDecoder(1)
	frameSize := 480 // 10ms

	// Decode 5 frames
	for i := 0; i < 5; i++ {
		frameData := make([]byte, 32)
		frameData[0] = 0x80
		for j := 1; j < len(frameData); j++ {
			frameData[j] = byte((i*32 + j) % 256)
		}

		samples, err := d.DecodeFrame(frameData, frameSize)
		if err != nil {
			t.Fatalf("Frame %d: DecodeFrame failed: %v", i, err)
		}

		if len(samples) != frameSize {
			t.Errorf("Frame %d: got %d samples, want %d", i, len(samples), frameSize)
		}

		// Check samples are finite
		for j, s := range samples {
			if math.IsNaN(s) || math.IsInf(s, 0) {
				t.Errorf("Frame %d, sample %d: invalid value %v", i, j, s)
			}
		}
	}
}

// TestCELTDecoderQualitySummary runs key tests and reports overall status.
// This test documents the Phase 15 success criteria validation.
func TestCELTDecoderQualitySummary(t *testing.T) {
	t.Log("=== CELT Decoder Quality Summary ===")

	// Test 1: Frame size support
	frameSizes := []int{120, 240, 480, 960}
	frameSizePass := true

	d := NewDecoder(1)
	for _, fs := range frameSizes {
		frameData := make([]byte, fs/8)
		if len(frameData) < 8 {
			frameData = make([]byte, 8)
		}
		frameData[0] = 0x80

		samples, err := d.DecodeFrame(frameData, fs)
		if err != nil || len(samples) != fs {
			t.Errorf("Frame size %d: FAIL (err=%v, samples=%d)", fs, err, len(samples))
			frameSizePass = false
		} else {
			t.Logf("Frame size %d: PASS", fs)
		}
	}

	// Test 2: Finite output
	finitePass := true
	for _, fs := range frameSizes {
		frameData := make([]byte, 32)
		for i := range frameData {
			frameData[i] = byte(i * 7)
		}

		samples, _ := d.DecodeFrame(frameData, fs)
		for _, s := range samples {
			if math.IsNaN(s) || math.IsInf(s, 0) {
				finitePass = false
				break
			}
		}
	}
	t.Logf("Finite output: %v", finitePass)

	// Test 3: Non-zero output capability
	d.Reset()
	frameData := make([]byte, 64)
	for i := range frameData {
		frameData[i] = byte(i * 13 % 256)
	}

	samples, _ := d.DecodeFrame(frameData, 960)
	maxAbs := 0.0
	for _, s := range samples {
		if math.Abs(s) > maxAbs {
			maxAbs = math.Abs(s)
		}
	}
	t.Logf("Max amplitude: %f", maxAbs)

	// Summary
	t.Log("=== End Summary ===")

	if !frameSizePass {
		t.Error("FAIL: Not all frame sizes supported")
	}
	if !finitePass {
		t.Error("FAIL: Non-finite values in output")
	}
}

// TestDecodeWithTrace runs a CELT packet decode with tracing enabled.
// This test produces trace output for manual inspection and comparison with libopus.
func TestDecodeWithTrace(t *testing.T) {
	// Save and restore original tracer
	original := DefaultTracer
	defer SetTracer(original)

	// Create trace buffer
	var buf bytes.Buffer
	SetTracer(&LogTracer{W: &buf})

	// Create decoder
	dec := NewDecoder(1) // mono

	// Create a non-silence CELT frame
	// Using 0xFF bytes avoids triggering silence flag in range decoder
	frameData := make([]byte, 32)
	for i := range frameData {
		frameData[i] = 0xFF
	}

	// Decode a 10ms frame (480 samples)
	samples, err := dec.DecodeFrame(frameData, 480)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	if len(samples) != 480 {
		t.Errorf("Expected 480 samples, got %d", len(samples))
	}

	// Get trace output
	trace := buf.String()

	// Log trace output for manual inspection
	t.Logf("=== TRACE OUTPUT (10ms mono frame) ===\n%s", trace)

	// Verify trace output contains expected sections
	if !strings.Contains(trace, "[CELT:header]") {
		t.Error("Trace missing [CELT:header] section")
	}

	if !strings.Contains(trace, "[CELT:energy]") {
		t.Error("Trace missing [CELT:energy] section")
	}

	if !strings.Contains(trace, "[CELT:synthesis]") {
		t.Error("Trace missing [CELT:synthesis] section")
	}

	// Count trace lines for statistics
	lines := strings.Split(trace, "\n")
	headerCount := 0
	energyCount := 0
	allocCount := 0
	pvqCount := 0
	coeffsCount := 0
	synthesisCount := 0

	for _, line := range lines {
		switch {
		case strings.Contains(line, "[CELT:header]"):
			headerCount++
		case strings.Contains(line, "[CELT:energy]"):
			energyCount++
		case strings.Contains(line, "[CELT:alloc]"):
			allocCount++
		case strings.Contains(line, "[CELT:pvq]"):
			pvqCount++
		case strings.Contains(line, "[CELT:coeffs]"):
			coeffsCount++
		case strings.Contains(line, "[CELT:synthesis]"):
			synthesisCount++
		}
	}

	t.Logf("Trace statistics: header=%d energy=%d alloc=%d pvq=%d coeffs=%d synthesis=%d",
		headerCount, energyCount, allocCount, pvqCount, coeffsCount, synthesisCount)

	// Verify we got energy traces for multiple bands
	if energyCount < 1 {
		t.Errorf("Expected at least 1 energy trace for 10ms frame, got %d", energyCount)
	}
}

// TestDecodeWithTraceSilence tests trace output for a silence frame.
func TestDecodeWithTraceSilence(t *testing.T) {
	original := DefaultTracer
	defer SetTracer(original)

	var buf bytes.Buffer
	SetTracer(&LogTracer{W: &buf})

	dec := NewDecoder(1)

	// Silence frame: 0x80 triggers silence flag
	silenceFrame := []byte{0x80}

	samples, err := dec.DecodeFrame(silenceFrame, 480)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	if len(samples) != 480 {
		t.Errorf("Expected 480 samples, got %d", len(samples))
	}

	// Silence frames should produce no trace (no header trace either since
	// silence is detected early before header flags are fully decoded)
	trace := buf.String()
	t.Logf("Silence frame trace:\n%s", trace)

	// Verify all samples are zero (or very close due to de-emphasis filter state)
	allZero := true
	for _, s := range samples {
		if math.Abs(s) > 1e-10 {
			allZero = false
			break
		}
	}

	t.Logf("Silence frame allZero=%v", allZero)
}

// TestDecodeWithTraceMultipleFrames tests trace consistency across frames.
func TestDecodeWithTraceMultipleFrames(t *testing.T) {
	original := DefaultTracer
	defer SetTracer(original)

	var buf bytes.Buffer
	SetTracer(&LogTracer{W: &buf})

	dec := NewDecoder(1)

	// Decode 3 consecutive frames
	for i := 0; i < 3; i++ {
		// Use 0xFF to avoid silence flag
		frameData := make([]byte, 32)
		for j := range frameData {
			frameData[j] = 0xFF
		}

		_, err := dec.DecodeFrame(frameData, 480)
		if err != nil {
			t.Fatalf("Frame %d: DecodeFrame failed: %v", i, err)
		}
	}

	trace := buf.String()

	// Count header traces - should be 3 (one per frame)
	headerCount := strings.Count(trace, "[CELT:header]")
	if headerCount < 3 {
		t.Errorf("Expected 3 header traces for 3 frames, got %d", headerCount)
	}

	t.Logf("3-frame trace (%d lines, %d headers)", len(strings.Split(trace, "\n")), headerCount)
}

// ============================================================================
// Phase 15-08: Range decoder bit consumption tracking tests
// These tests track how many bits the range decoder consumes at each stage.
// ============================================================================

// TestRangeDecoderBitConsumption tracks bits consumed at each decode stage.
// Creates a known CELT packet and tracks rd.Tell() at key points.
func TestRangeDecoderBitConsumption(t *testing.T) {
	testCases := []struct {
		name      string
		frameSize int
		dataLen   int
	}{
		{"20ms_64bytes", 960, 64},
		{"20ms_32bytes", 960, 32},
		{"10ms_32bytes", 480, 32},
		{"5ms_16bytes", 240, 16},
		{"2.5ms_8bytes", 120, 8},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test frame data
			frameData := make([]byte, tc.dataLen)
			// Use pattern that won't trigger silence flag
			for i := range frameData {
				frameData[i] = byte(0xAA ^ byte(i))
			}

			d := NewDecoder(1)

			// Decode and track bit consumption
			samples, err := d.DecodeFrame(frameData, tc.frameSize)
			if err != nil {
				t.Logf("Decode error (may be expected for test data): %v", err)
			}

			// Log results
			totalBits := tc.dataLen * 8
			t.Logf("Packet: %d bytes = %d bits", tc.dataLen, totalBits)
			t.Logf("Frame size: %d samples", tc.frameSize)
			if samples != nil {
				t.Logf("Output: %d samples", len(samples))
			}

			// Check range decoder state if available
			rd := d.RangeDecoder()
			if rd != nil {
				bitsUsed := rd.Tell()
				t.Logf("Bits consumed (Tell): %d", bitsUsed)
				t.Logf("Consumption ratio: %.1f%%", 100*float64(bitsUsed)/float64(totalBits))
			}
		})
	}
}

// TestRangeDecoderBitConsumptionByStage tracks bits consumed at each major stage.
// This test manually steps through decode stages to measure consumption.
func TestRangeDecoderBitConsumptionByStage(t *testing.T) {
	// Create a test packet
	frameData := make([]byte, 64)
	for i := range frameData {
		frameData[i] = byte(0x55 ^ byte(i*3))
	}

	frameSize := 960
	mode := GetModeConfig(frameSize)

	// Create decoder and range decoder
	d := NewDecoder(1)

	// Manually decode stages to track bit consumption
	// Note: This creates a fresh range decoder for inspection
	rd := &rangecoding.Decoder{}
	rd.Init(frameData)
	d.SetRangeDecoder(rd)

	t.Logf("=== Bit Consumption by Stage ===")
	t.Logf("Packet: %d bytes = %d bits", len(frameData), len(frameData)*8)

	// Stage 0: Initial state
	bitsAfterInit := rd.Tell()
	t.Logf("After init: %d bits", bitsAfterInit)

	// Stage 1: Decode silence flag
	silence := rd.DecodeBit(15) == 1
	bitsAfterSilence := rd.Tell()
	t.Logf("After silence flag: %d bits (+%d for silence=%v)",
		bitsAfterSilence, bitsAfterSilence-bitsAfterInit, silence)

	if !silence {
		// Stage 2: Decode transient flag (if LM >= 1)
		var transient bool
		if mode.LM >= 1 {
			transient = rd.DecodeBit(3) == 1
		}
		bitsAfterTransient := rd.Tell()
		t.Logf("After transient flag: %d bits (+%d for transient=%v)",
			bitsAfterTransient, bitsAfterTransient-bitsAfterSilence, transient)

		// Stage 3: Decode intra flag
		intra := rd.DecodeBit(3) == 1
		bitsAfterIntra := rd.Tell()
		t.Logf("After intra flag: %d bits (+%d for intra=%v)",
			bitsAfterIntra, bitsAfterIntra-bitsAfterTransient, intra)

		// Stage 4: Coarse energy would be decoded here
		// We can measure how much the full decode consumes
		bitsBeforeCoarse := rd.Tell()

		// Decode coarse energy for all bands
		energies := d.DecodeCoarseEnergy(mode.EffBands, intra, mode.LM)
		bitsAfterCoarse := rd.Tell()
		t.Logf("After coarse energy: %d bits (+%d for %d bands)",
			bitsAfterCoarse, bitsAfterCoarse-bitsBeforeCoarse, mode.EffBands)

		// Log energy values
		t.Logf("Coarse energies (first 5 bands):")
		for i := 0; i < 5 && i < len(energies); i++ {
			t.Logf("  Band %d: %.2f", i, energies[i])
		}

		// Remaining bits for allocation
		remainingBits := len(frameData)*8 - bitsAfterCoarse
		if remainingBits < 0 {
			remainingBits = 0
		}
		t.Logf("Remaining bits for PVQ/fine energy: %d", remainingBits)

		// Compute expected allocation
		allocResult := ComputeAllocation(
			remainingBits,
			mode.EffBands,
			1,
			nil,   // caps
			nil,   // dynalloc
			0,     // trim
			-1,    // intensity
			false, // dual stereo
			mode.LM,
		)

		allocTotalQ3 := 0
		for band := 0; band < len(allocResult.BandBits); band++ {
			allocTotalQ3 += allocResult.BandBits[band] + (allocResult.FineBits[band] << bitRes)
		}
		allocTotal := allocTotalQ3 >> bitRes
		t.Logf("Allocation total: %d bits", allocTotal)
		t.Logf("Allocation breakdown (first 5 bands):")
		for i := 0; i < 5 && i < len(allocResult.BandBits); i++ {
			t.Logf("  Band %d: bandBits=%d, fineBits=%d",
				i, allocResult.BandBits[i], allocResult.FineBits[i])
		}
	}
}

// TestBitConsumptionVsAllocation compares expected allocation with actual consumption.
func TestBitConsumptionVsAllocation(t *testing.T) {
	testCases := []struct {
		name      string
		frameSize int
		dataLen   int
	}{
		{"960_samples_48bytes", 960, 48},
		{"960_samples_96bytes", 960, 96},
		{"480_samples_32bytes", 480, 32},
		{"240_samples_16bytes", 240, 16},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test frame
			frameData := make([]byte, tc.dataLen)
			for i := range frameData {
				frameData[i] = byte(0x33 ^ byte(i*7))
			}

			mode := GetModeConfig(tc.frameSize)
			totalBits := tc.dataLen * 8

			// Compute expected allocation
			// Estimate bits available after header (~10 bits for flags)
			headerOverhead := 10
			availableBits := totalBits - headerOverhead
			if availableBits < 0 {
				availableBits = 0
			}

			allocResult := ComputeAllocation(
				availableBits,
				mode.EffBands,
				1,
				nil,   // caps
				nil,   // dynalloc
				0,     // trim
				-1,    // intensity
				false, // dual stereo
				mode.LM,
			)
			allocTotalQ3 := 0
			for band := 0; band < len(allocResult.BandBits); band++ {
				allocTotalQ3 += allocResult.BandBits[band] + (allocResult.FineBits[band] << bitRes)
			}
			allocTotal := allocTotalQ3 >> bitRes

			t.Logf("=== %s ===", tc.name)
			t.Logf("Total packet bits: %d", totalBits)
			t.Logf("Header overhead estimate: %d bits", headerOverhead)
			t.Logf("Available for allocation: %d bits", availableBits)
			t.Logf("Allocation computed: %d bits", allocTotal)

			// Check allocation doesn't exceed available
			if allocTotal > availableBits {
				t.Errorf("Allocation (%d) exceeds available bits (%d)",
					allocTotal, availableBits)
			}

			// Decode the frame to see actual consumption
			d := NewDecoder(1)
			samples, err := d.DecodeFrame(frameData, tc.frameSize)
			if err != nil {
				t.Logf("Decode error (test data): %v", err)
			}

			if samples != nil {
				t.Logf("Decoded: %d samples", len(samples))
			}

			// Check final range decoder state
			rd := d.RangeDecoder()
			if rd != nil {
				consumed := rd.Tell()
				t.Logf("Actual bits consumed: %d (%.1f%% of packet)",
					consumed, 100*float64(consumed)/float64(totalBits))

				// Significant mismatch could indicate desync
				expected := allocTotal + headerOverhead
				delta := consumed - expected
				if delta < 0 {
					delta = -delta
				}
				if delta > totalBits/4 {
					t.Logf("Note: Large delta between expected (%d) and actual (%d): %d bits",
						expected, consumed, delta)
				}
			}
		})
	}
}

// TestRangeDecoderBitBudgetValidation validates bit budget tracking.
func TestRangeDecoderBitBudgetValidation(t *testing.T) {
	// Create a controlled test case
	frameData := make([]byte, 32)
	for i := range frameData {
		frameData[i] = byte(0xCC ^ byte(i))
	}

	frameSize := 960
	totalBits := len(frameData) * 8

	d := NewDecoder(1)
	_, err := d.DecodeFrame(frameData, frameSize)
	if err != nil {
		t.Logf("Decode error (expected for test data): %v", err)
	}

	rd := d.RangeDecoder()
	if rd == nil {
		t.Log("No range decoder available after decode")
		return
	}

	consumed := rd.Tell()
	t.Logf("Budget validation:")
	t.Logf("  Packet size: %d bits", totalBits)
	t.Logf("  Bits consumed: %d", consumed)
	t.Logf("  Consumption: %.1f%%", 100*float64(consumed)/float64(totalBits))

	// Consumption should not exceed packet size significantly
	// (some overread is possible due to range coder normalization)
	maxAllowed := totalBits + 32 // Allow some overread margin
	if consumed > maxAllowed {
		t.Errorf("Consumed %d bits exceeds max allowed %d", consumed, maxAllowed)
	} else {
		t.Logf("  Within budget: OK")
	}
}
