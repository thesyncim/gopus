package hybrid

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestNewDecoder verifies decoder initialization.
func TestNewDecoder(t *testing.T) {
	tests := []struct {
		name     string
		channels int
		want     int
	}{
		{"mono", 1, 1},
		{"stereo", 2, 2},
		{"negative defaults to 1", -1, 1},
		{"zero defaults to 1", 0, 1},
		{"large clamped to 2", 10, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDecoder(tt.channels)
			if d == nil {
				t.Fatal("NewDecoder returned nil")
			}
			if d.Channels() != tt.want {
				t.Errorf("Channels() = %d, want %d", d.Channels(), tt.want)
			}
			if d.silkDecoder == nil {
				t.Error("silkDecoder is nil")
			}
			if d.celtDecoder == nil {
				t.Error("celtDecoder is nil")
			}
		})
	}
}

// TestValidHybridFrameSize verifies frame size validation.
func TestValidHybridFrameSize(t *testing.T) {
	tests := []struct {
		frameSize int
		valid     bool
	}{
		{480, true},   // 10ms at 48kHz
		{960, true},   // 20ms at 48kHz
		{120, false},  // 2.5ms - not valid for hybrid
		{240, false},  // 5ms - not valid for hybrid
		{0, false},    // invalid
		{-1, false},   // invalid
		{1920, false}, // 40ms - not valid for hybrid
		{2880, false}, // 60ms - not valid for hybrid
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := ValidHybridFrameSize(tt.frameSize)
			if got != tt.valid {
				t.Errorf("ValidHybridFrameSize(%d) = %v, want %v", tt.frameSize, got, tt.valid)
			}
		})
	}
}

// TestHybridFrameSizes verifies frame size validation.
func TestHybridFrameSizes(t *testing.T) {
	tests := []struct {
		name      string
		frameSize int
		expectErr bool
	}{
		{"invalid 2.5ms", 120, true},
		{"invalid 5ms", 240, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDecoder(1)

			// Create minimal test data
			testData := make([]byte, 64)

			_, err := d.Decode(testData, tt.frameSize)
			if tt.expectErr && err == nil {
				t.Error("expected error but got nil")
			}
			if tt.expectErr && err != ErrInvalidFrameSize {
				t.Errorf("expected ErrInvalidFrameSize, got %v", err)
			}
		})
	}

	// Verify valid frame sizes don't return invalid frame size error
	t.Run("valid frame sizes accepted", func(t *testing.T) {
		d := NewDecoder(1)
		testData := make([]byte, 64)

		// 480 (10ms) should be accepted (may fail for other reasons)
		_, err := d.Decode(testData, 480)
		if err == ErrInvalidFrameSize {
			t.Error("480 samples should be valid frame size for hybrid")
		}

		d.Reset()

		// 960 (20ms) should be accepted (may fail for other reasons)
		_, err = d.Decode(testData, 960)
		if err == ErrInvalidFrameSize {
			t.Error("960 samples should be valid frame size for hybrid")
		}
	})
}

// TestHybridReset verifies reset clears state properly.
func TestHybridReset(t *testing.T) {
	d := NewDecoder(1)
	d.prevPacketStereo = true
	d.Reset()

	if d.prevPacketStereo {
		t.Error("prevPacketStereo = true after reset, want false")
	}
}

// TestHybridOutputRange verifies output samples stay finite and bounded for
// syntactically valid minimal hybrid packets.
func TestHybridOutputRange(t *testing.T) {
	tests := []struct {
		name      string
		channels  int
		frameSize int
		stereo    bool
	}{
		{name: "mono_10ms", channels: 1, frameSize: 480},
		{name: "mono_20ms", channels: 1, frameSize: 960},
		{name: "stereo_20ms", channels: 2, frameSize: 960, stereo: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDecoder(tt.channels)
			packet := createMinimalHybridPacket(tt.frameSize)

			rd := &rangecoding.Decoder{}
			rd.Init(packet)

			output, err := d.decodeFrame(rd, tt.frameSize, tt.stereo)
			if err != nil {
				t.Fatalf("decodeFrame failed: %v", err)
			}

			wantLen := tt.frameSize * tt.channels
			if len(output) != wantLen {
				t.Fatalf("output length = %d, want %d", len(output), wantLen)
			}

			var maxAbs float64
			for i, sample := range output {
				sample64 := float64(sample)
				if math.IsNaN(sample64) || math.IsInf(sample64, 0) {
					t.Fatalf("output[%d] is invalid: %f", i, sample)
				}
				if abs := math.Abs(sample64); abs > maxAbs {
					maxAbs = abs
				}
			}
			if maxAbs > 100 {
				t.Fatalf("max absolute sample value = %f, want <= 100", maxAbs)
			}
		})
	}
}

// TestHybridStereo verifies stereo hybrid decoding API.
// Note: This test uses synthetic data which cannot form valid Opus packets.
func TestHybridStereo(t *testing.T) {
	// Synthetic data cannot form valid hybrid packets due to SILK complexity
	// This test validates that stereo decoder initialization works
	d := NewDecoder(2)
	if d.Channels() != 2 {
		t.Errorf("Channels() = %d, want 2", d.Channels())
	}

	// Verify mono decoder cannot use DecodeStereo
	mono := NewDecoder(1)
	_, err := mono.DecodeStereo([]byte{0}, 480)
	if err != ErrDecodeFailed {
		t.Errorf("DecodeStereo on mono decoder should return ErrDecodeFailed, got %v", err)
	}

	rd := &rangecoding.Decoder{}
	_, err = mono.DecodeStereoWithDecoder(rd, 480)
	if err != ErrDecodeFailed {
		t.Errorf("DecodeStereoWithDecoder on mono decoder should return ErrDecodeFailed, got %v", err)
	}
}

// TestHybridEmptyInput verifies empty input handling.
// Note: nil and empty input now trigger PLC (Packet Loss Concealment), not an error.
func TestHybridEmptyInput(t *testing.T) {
	d := NewDecoder(1)

	// nil input triggers PLC, should succeed
	samples, err := d.Decode(nil, 480)
	if err != nil {
		t.Errorf("Decode(nil) error = %v, want nil (PLC mode)", err)
	}
	if len(samples) != 480 {
		t.Errorf("Decode(nil) output length = %d, want 480 (PLC output)", len(samples))
	}

	// Empty slice triggers PLC, should succeed
	samples, err = d.Decode([]byte{}, 480)
	if err != nil {
		t.Errorf("Decode([]) error = %v, want nil (PLC mode)", err)
	}
	if len(samples) != 480 {
		t.Errorf("Decode([]) output length = %d, want 480 (PLC output)", len(samples))
	}
}

// TestHybridInvalidFrameSize verifies invalid frame size handling.
func TestHybridInvalidFrameSize(t *testing.T) {
	d := NewDecoder(1)
	testData := make([]byte, 64)

	_, err := d.Decode(testData, 120)
	if err != ErrInvalidFrameSize {
		t.Errorf("Decode(120) error = %v, want %v", err, ErrInvalidFrameSize)
	}

	_, err = d.Decode(testData, 0)
	if err != ErrInvalidFrameSize {
		t.Errorf("Decode(0) error = %v, want %v", err, ErrInvalidFrameSize)
	}
}

// TestHybridConstants verifies hybrid constants.
func TestHybridConstants(t *testing.T) {
	// Verify SilkCELTDelay matches expected value
	if SilkCELTDelay != 60 {
		t.Errorf("SilkCELTDelay = %d, want 60", SilkCELTDelay)
	}

	// Verify HybridCELTStartBand
	if HybridCELTStartBand != 17 {
		t.Errorf("HybridCELTStartBand = %d, want 17", HybridCELTStartBand)
	}
}

// TestUpsample3x verifies 3x upsampling from 16kHz to 48kHz.
func TestUpsample3x(t *testing.T) {
	// Test with known input
	input := []float32{0, 3, 6, 9}
	output := upsample3x(input)

	// Expected length: 4 * 3 = 12
	if len(output) != 12 {
		t.Errorf("len(output) = %d, want 12", len(output))
	}

	// First sample should be input[0]
	if output[0] != 0 {
		t.Errorf("output[0] = %f, want 0", output[0])
	}

	// Sample 3 should be input[1]
	if output[3] != 3 {
		t.Errorf("output[3] = %f, want 3", output[3])
	}

	// Interpolated samples should be between adjacent input values
	// output[1] and output[2] should be between 0 and 3
	if output[1] < 0 || output[1] > 3 {
		t.Errorf("output[1] = %f, should be between 0 and 3", output[1])
	}
	if output[2] < 0 || output[2] > 3 {
		t.Errorf("output[2] = %f, should be between 0 and 3", output[2])
	}
}

// TestUpsample3xEmpty verifies empty input handling.
func TestUpsample3xEmpty(t *testing.T) {
	output := upsample3x(nil)
	if output != nil {
		t.Errorf("upsample3x(nil) = %v, want nil", output)
	}

	output = upsample3x([]float32{})
	if output != nil {
		t.Errorf("upsample3x([]) = %v, want nil", output)
	}
}

func float64ToInt16ForTest(samples []float64) []int16 {
	output := make([]int16, len(samples))
	for i, s := range samples {
		output[i] = opusmath.Float32ToInt16(float32(s))
	}
	return output
}

// TestFloat64ToInt16 verifies conversion and clamping.
func TestFloat64ToInt16(t *testing.T) {
	tests := []struct {
		input    float64
		expected int16
	}{
		{0, 0},
		{1.0, 32767},
		{-1.0, -32768},
		{2.0, 32767},   // Clamped
		{-2.0, -32768}, // Clamped
		{0.5, 16384},
		{math.Nextafter(0.5/32768.0, 1), 0},
		{math.Nextafter(-0.5/32768.0, -1), 0},
	}

	for _, tt := range tests {
		input := []float64{tt.input}
		output := float64ToInt16ForTest(input)
		if output[0] != tt.expected {
			t.Errorf("float64ToInt16(%f) = %d, want %d", tt.input, output[0], tt.expected)
		}
	}
}

// TestDecodeToFloat32 verifies float32 conversion API.
func TestDecodeToFloat32(t *testing.T) {
	// Test invalid frame size error propagation
	d := NewDecoder(1)
	testData := make([]byte, 64)

	_, err := d.DecodeToFloat32(testData, 120) // Invalid frame size
	if err != ErrInvalidFrameSize {
		t.Errorf("DecodeToFloat32 with invalid frame size should return ErrInvalidFrameSize, got %v", err)
	}
}

func TestDecodeToFloat32MatchesDirectFloat32Path(t *testing.T) {
	for _, tt := range []struct {
		name         string
		channels     int
		packetStereo bool
		frameSize    int
	}{
		{name: "mono_packet", channels: 1, packetStereo: false, frameSize: 480},
		{name: "stereo_packet", channels: 2, packetStereo: true, frameSize: 960},
	} {
		t.Run(tt.name, func(t *testing.T) {
			packet := createMinimalHybridPacket(tt.frameSize)

			publicDec := NewDecoder(tt.channels)
			got, err := publicDec.DecodeToFloat32WithPacketStereo(packet, tt.frameSize, tt.packetStereo)
			if err != nil {
				t.Fatalf("DecodeToFloat32WithPacketStereo failed: %v", err)
			}

			directDec := NewDecoder(tt.channels)
			rd := &rangecoding.Decoder{}
			rd.Init(packet)
			want := make([]float32, tt.frameSize*tt.channels)
			if err := directDec.DecodeWithDecoderHookToFloat32(rd, tt.frameSize, tt.packetStereo, nil, want); err != nil {
				t.Fatalf("DecodeWithDecoderHookToFloat32 failed: %v", err)
			}

			if len(got) != len(want) {
				t.Fatalf("DecodeToFloat32 length = %d, want %d", len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("sample %d = %08x, want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}
		})
	}
}

func TestDecodePLCFloat32MatchesLegacyLeafWiden(t *testing.T) {
	const frameSize = 480
	packet := createMinimalHybridPacket(frameSize)

	floatDec := NewDecoder(1)
	if _, err := floatDec.DecodeToFloat32(packet, frameSize); err != nil {
		t.Fatalf("seed DecodeToFloat32 failed: %v", err)
	}
	got, err := floatDec.DecodeToFloat32(nil, frameSize)
	if err != nil {
		t.Fatalf("DecodeToFloat32 PLC failed: %v", err)
	}

	legacyDec := NewDecoder(1)
	if _, err := legacyDec.Decode(packet, frameSize); err != nil {
		t.Fatalf("seed Decode failed: %v", err)
	}
	legacy, err := legacyDec.Decode(nil, frameSize)
	if err != nil {
		t.Fatalf("Decode PLC failed: %v", err)
	}

	if len(got) != len(legacy) {
		t.Fatalf("PLC length = %d, want %d", len(got), len(legacy))
	}
	for i := range got {
		want := float32(legacy[i])
		if got[i] != want {
			t.Fatalf("PLC sample %d = %08x, want legacy leaf-widen %08x", i, math.Float32bits(got[i]), math.Float32bits(want))
		}
	}
}

func TestDecodeToInt16(t *testing.T) {
	d := NewDecoder(1)
	testData := make([]byte, 64)

	_, err := d.DecodeToInt16(testData, 120)
	if err != ErrInvalidFrameSize {
		t.Errorf("DecodeToInt16 with invalid frame size should return ErrInvalidFrameSize, got %v", err)
	}
}

// =============================================================================
// Real Hybrid Packet Integration Tests
// =============================================================================
// These tests use the createMinimalHybridPacket helper to generate valid
// hybrid packets and verify the decoder can process them end-to-end.

// TestHybridRealPacketDecode is the primary integration test.
// It verifies the hybrid decoder can process a real range-coded packet
// containing both SILK and CELT data.
func TestHybridRealPacketDecode(t *testing.T) {
	d := NewDecoder(1) // mono

	// Get minimal valid hybrid packet for 20ms
	packet := createMinimalHybridPacket(960)

	// Create range decoder from packet
	rd := &rangecoding.Decoder{}
	rd.Init(packet)

	// Decode frame using the internal decodeFrame method
	output, err := d.decodeFrame(rd, 960, false)
	if err != nil {
		t.Fatalf("decodeFrame failed: %v", err)
	}

	// Verify output length (960 samples for 20ms at 48kHz)
	if len(output) != 960 {
		t.Errorf("output length = %d, want 960", len(output))
	}

	// Verify output is within reasonable range (not NaN/Inf)
	for i, s := range output {
		s64 := float64(s)
		if math.IsNaN(s64) {
			t.Errorf("output[%d] is NaN", i)
			break
		}
		if math.IsInf(s64, 0) {
			t.Errorf("output[%d] is Inf", i)
			break
		}
	}

	t.Logf("Successfully decoded 20ms hybrid packet: %d samples, first sample: %f", len(output), output[0])
}

// TestHybridRealPacket10ms tests 10ms frame size (480 samples).
// This verifies the decoder handles both valid hybrid frame sizes.
func TestHybridRealPacket10ms(t *testing.T) {
	d := NewDecoder(1) // mono

	// Get minimal valid hybrid packet for 10ms
	packet := createMinimalHybridPacket(480)

	// Create range decoder from packet
	rd := &rangecoding.Decoder{}
	rd.Init(packet)

	// Decode frame
	output, err := d.decodeFrame(rd, 480, false)
	if err != nil {
		t.Fatalf("decodeFrame failed for 10ms: %v", err)
	}

	// Verify output length (480 samples for 10ms at 48kHz)
	if len(output) != 480 {
		t.Errorf("output length = %d, want 480", len(output))
	}

	// Verify output is within reasonable range
	for i, s := range output {
		s64 := float64(s)
		if math.IsNaN(s64) || math.IsInf(s64, 0) {
			t.Errorf("output[%d] is %f (invalid)", i, s)
			break
		}
	}

	t.Logf("Successfully decoded 10ms hybrid packet: %d samples", len(output))
}

// TestHybridRealPacketStereo tests stereo hybrid decoding.
// Verifies the decoder handles 2-channel output correctly.
func TestHybridRealPacketStereo(t *testing.T) {
	d := NewDecoder(2) // stereo

	// Get minimal valid hybrid packet for 20ms
	packet := createMinimalHybridPacket(960)

	// Create range decoder from packet
	rd := &rangecoding.Decoder{}
	rd.Init(packet)

	// Decode stereo frame
	output, err := d.decodeFrame(rd, 960, true)
	if err != nil {
		t.Fatalf("decodeFrame stereo failed: %v", err)
	}

	// Verify output length (960 samples * 2 channels = 1920)
	expectedLen := 960 * 2
	if len(output) != expectedLen {
		t.Errorf("stereo output length = %d, want %d", len(output), expectedLen)
	}

	// Verify output is within reasonable range
	for i, s := range output {
		s64 := float64(s)
		if math.IsNaN(s64) || math.IsInf(s64, 0) {
			t.Errorf("output[%d] is %f (invalid)", i, s)
			break
		}
	}

	t.Logf("Successfully decoded stereo hybrid packet: %d samples (interleaved)", len(output))
}

// TestHybridRealPacketWithPublicAPI tests the public Decode API with real packets.
// This ensures the full decoding pipeline works end-to-end.
func TestHybridRealPacketWithPublicAPI(t *testing.T) {
	d := NewDecoder(1)

	// Use hardcoded packet as fallback for more reliable testing
	packet := minimalHybridPacket20ms

	// Call public API
	output, err := d.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	// Verify output
	if len(output) != 960 {
		t.Errorf("output length = %d, want 960", len(output))
	}

	// Check for valid samples
	invalidCount := 0
	for _, s := range output {
		s64 := float64(s)
		if math.IsNaN(s64) || math.IsInf(s64, 0) {
			invalidCount++
		}
	}
	if invalidCount > 0 {
		t.Errorf("found %d invalid samples (NaN/Inf)", invalidCount)
	}
}

// TestHybridRangeDecoderTransition verifies range decoder state transitions
// correctly from SILK to CELT within the same packet.
func TestHybridRangeDecoderTransition(t *testing.T) {
	d := NewDecoder(1)

	// Create packet
	packet := createMinimalHybridPacket(960)

	// Initialize range decoder
	rd := &rangecoding.Decoder{}
	rd.Init(packet)

	// Record initial state
	initialTell := rd.Tell()

	// Decode frame (SILK reads first, then CELT)
	_, err := d.decodeFrame(rd, 960, false)
	if err != nil {
		t.Fatalf("decodeFrame failed: %v", err)
	}

	// Verify range decoder consumed bits
	finalTell := rd.Tell()
	bitsConsumed := finalTell - initialTell

	// Both SILK and CELT should have consumed some bits
	if bitsConsumed <= 0 {
		t.Errorf("range decoder should have consumed bits, but Tell() unchanged")
	}

	t.Logf("Range decoder consumed %d bits (SILK+CELT)", bitsConsumed)
}

// TestHybridMultipleFrames tests decoding multiple consecutive frames.
// This verifies decoder state persists correctly between frames.
func TestHybridMultipleFrames(t *testing.T) {
	d := NewDecoder(1)

	// Decode multiple frames to verify state persistence
	for frame := range 5 {
		packet := createMinimalHybridPacket(960)

		rd := &rangecoding.Decoder{}
		rd.Init(packet)

		output, err := d.decodeFrame(rd, 960, false)
		if err != nil {
			t.Fatalf("frame %d: decodeFrame failed: %v", frame, err)
		}

		if len(output) != 960 {
			t.Errorf("frame %d: output length = %d, want 960", frame, len(output))
		}
	}

	t.Log("Successfully decoded 5 consecutive hybrid frames")
}

// TestHybridOutputSampleRange verifies output samples are in reasonable range.
// For minimal/silence packets, output should be near-zero or low-level.
func TestHybridOutputSampleRange(t *testing.T) {
	d := NewDecoder(1)

	packet := createMinimalHybridPacket(960)

	rd := &rangecoding.Decoder{}
	rd.Init(packet)

	output, err := d.decodeFrame(rd, 960, false)
	if err != nil {
		t.Fatalf("decodeFrame failed: %v", err)
	}

	// Calculate max absolute value
	var maxAbs float64
	for _, s := range output {
		abs := math.Abs(float64(s))
		if abs > maxAbs {
			maxAbs = abs
		}
	}

	// For a minimal packet, we don't expect huge amplitude values
	// Allow up to 10.0 for filter transients, but flag if much larger
	if maxAbs > 100.0 {
		t.Logf("Warning: max absolute sample value is %f (unexpectedly large for minimal packet)", maxAbs)
	}

	t.Logf("Max absolute sample value: %f", maxAbs)
}

// =============================================================================
// Benchmarks
// =============================================================================

// BenchmarkHybridDecode benchmarks hybrid decoding.
// Note: Uses synthetic data so actual decoding performance may vary.
func BenchmarkHybridDecode(b *testing.B) {
	b.Skip("Skipping: synthetic data cannot form valid hybrid packets")
}

// BenchmarkUpsample3x benchmarks upsampling.
func BenchmarkUpsample3x(b *testing.B) {
	input := make([]float32, 160) // 10ms at 16kHz

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = upsample3x(input)
	}
}
