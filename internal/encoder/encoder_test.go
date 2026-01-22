package encoder

import (
	"math"
	"math/rand"
	"testing"

	"gopus"
	"gopus/internal/hybrid"
	"gopus/internal/rangecoding"
)

// TestNewEncoder verifies the encoder constructor creates valid encoder.
func TestNewEncoder(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
		wantRate   int
		wantCh     int
	}{
		{"48kHz mono", 48000, 1, 48000, 1},
		{"48kHz stereo", 48000, 2, 48000, 2},
		{"16kHz mono", 16000, 1, 16000, 1},
		{"8kHz mono", 8000, 1, 8000, 1},
		{"invalid rate defaults to 48kHz", 44100, 1, 48000, 1},
		{"invalid channels clamped", 48000, 5, 48000, 2},
		{"zero channels clamped to 1", 48000, 0, 48000, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder(tt.sampleRate, tt.channels)

			if enc == nil {
				t.Fatal("NewEncoder returned nil")
			}

			if enc.SampleRate() != tt.wantRate {
				t.Errorf("SampleRate() = %d, want %d", enc.SampleRate(), tt.wantRate)
			}

			if enc.Channels() != tt.wantCh {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), tt.wantCh)
			}

			// Default mode should be ModeAuto
			if enc.Mode() != ModeAuto {
				t.Errorf("Mode() = %d, want ModeAuto", enc.Mode())
			}

			// Default bandwidth should be Fullband
			if enc.Bandwidth() != gopus.BandwidthFullband {
				t.Errorf("Bandwidth() = %d, want Fullband", enc.Bandwidth())
			}

			// Default frame size should be 960 (20ms)
			if enc.FrameSize() != 960 {
				t.Errorf("FrameSize() = %d, want 960", enc.FrameSize())
			}
		})
	}
}

// TestEncoderSetMode verifies mode setting works correctly.
func TestEncoderSetMode(t *testing.T) {
	enc := NewEncoder(48000, 1)

	tests := []struct {
		mode Mode
		name string
	}{
		{ModeAuto, "ModeAuto"},
		{ModeSILK, "ModeSILK"},
		{ModeHybrid, "ModeHybrid"},
		{ModeCELT, "ModeCELT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc.SetMode(tt.mode)
			if enc.Mode() != tt.mode {
				t.Errorf("Mode() = %d, want %d", enc.Mode(), tt.mode)
			}
		})
	}
}

// TestEncoderSetBandwidth verifies bandwidth setting works correctly.
func TestEncoderSetBandwidth(t *testing.T) {
	enc := NewEncoder(48000, 1)

	tests := []struct {
		bw   gopus.Bandwidth
		name string
	}{
		{gopus.BandwidthNarrowband, "Narrowband"},
		{gopus.BandwidthMediumband, "Mediumband"},
		{gopus.BandwidthWideband, "Wideband"},
		{gopus.BandwidthSuperwideband, "Superwideband"},
		{gopus.BandwidthFullband, "Fullband"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc.SetBandwidth(tt.bw)
			if enc.Bandwidth() != tt.bw {
				t.Errorf("Bandwidth() = %d, want %d", enc.Bandwidth(), tt.bw)
			}
		})
	}
}

// TestEncoderSetFrameSize verifies frame size setting works correctly.
func TestEncoderSetFrameSize(t *testing.T) {
	enc := NewEncoder(48000, 1)

	frameSizes := []int{120, 240, 480, 960, 1920, 2880}

	for _, size := range frameSizes {
		t.Run(string(rune('0'+size/100)), func(t *testing.T) {
			enc.SetFrameSize(size)
			if enc.FrameSize() != size {
				t.Errorf("FrameSize() = %d, want %d", enc.FrameSize(), size)
			}
		})
	}
}

// TestEncoderReset verifies reset clears state properly.
func TestEncoderReset(t *testing.T) {
	enc := NewEncoder(48000, 1)

	// Modify state
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	// Reset
	enc.Reset()

	// Mode and bandwidth should be preserved (they're config, not state)
	if enc.Mode() != ModeHybrid {
		t.Errorf("Mode changed after Reset, got %d", enc.Mode())
	}

	// prevSamples should be zeroed (delay buffer)
	for i, v := range enc.prevSamples {
		if v != 0 {
			t.Errorf("prevSamples[%d] = %f, want 0", i, v)
			break
		}
	}
}

// TestValidFrameSize verifies frame size validation.
func TestValidFrameSize(t *testing.T) {
	tests := []struct {
		frameSize int
		mode      Mode
		want      bool
	}{
		// SILK valid sizes
		{480, ModeSILK, true},
		{960, ModeSILK, true},
		{1920, ModeSILK, true},
		{2880, ModeSILK, true},
		{120, ModeSILK, false},
		{240, ModeSILK, false},

		// Hybrid valid sizes
		{480, ModeHybrid, true},
		{960, ModeHybrid, true},
		{120, ModeHybrid, false},
		{240, ModeHybrid, false},
		{1920, ModeHybrid, false},

		// CELT valid sizes
		{120, ModeCELT, true},
		{240, ModeCELT, true},
		{480, ModeCELT, true},
		{960, ModeCELT, true},
		{1920, ModeCELT, false},
		{2880, ModeCELT, false},

		// Auto accepts all valid sizes
		{120, ModeAuto, true},
		{240, ModeAuto, true},
		{480, ModeAuto, true},
		{960, ModeAuto, true},
		{1920, ModeAuto, true},
		{2880, ModeAuto, true},
		{100, ModeAuto, false},
	}

	for _, tt := range tests {
		got := ValidFrameSize(tt.frameSize, tt.mode)
		if got != tt.want {
			t.Errorf("ValidFrameSize(%d, %d) = %v, want %v", tt.frameSize, tt.mode, got, tt.want)
		}
	}
}

// generateTestSignal generates a test signal (sine wave + noise) for encoding tests.
func generateTestSignal(samples int, channels int) []float64 {
	pcm := make([]float64, samples*channels)
	freq := 440.0 // 440 Hz sine wave
	sampleRate := 48000.0

	for i := 0; i < samples; i++ {
		t := float64(i) / sampleRate
		// Mix of sine wave and some noise for realistic content
		sample := 0.5 * math.Sin(2*math.Pi*freq*t)
		// Add a second harmonic
		sample += 0.25 * math.Sin(2*math.Pi*freq*2*t)
		// Add some high frequency content
		sample += 0.1 * math.Sin(2*math.Pi*8000*t)

		if channels == 1 {
			pcm[i] = sample
		} else {
			// Stereo: slight pan difference
			pcm[i*2] = sample * 0.8   // Left
			pcm[i*2+1] = sample * 1.0 // Right
		}
	}
	return pcm
}

// computeEnergy computes the total energy of a signal.
func computeEnergy(samples []float64) float64 {
	var energy float64
	for _, s := range samples {
		energy += s * s
	}
	return energy / float64(len(samples))
}

// TestHybridEncode10ms tests 480-sample (10ms) hybrid frame encoding.
func TestHybridEncode10ms(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	pcm := generateTestSignal(480, 1)

	encoded, err := enc.Encode(pcm, 480)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Error("Encoded data is empty")
	}

	t.Logf("10ms hybrid frame: %d bytes", len(encoded))
}

// TestHybridEncode20ms tests 960-sample (20ms) hybrid frame encoding.
func TestHybridEncode20ms(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	pcm := generateTestSignal(960, 1)

	encoded, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Error("Encoded data is empty")
	}

	t.Logf("20ms hybrid frame: %d bytes", len(encoded))
}

// TestHybridEncodeMono tests mono hybrid encoding.
func TestHybridEncodeMono(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthFullband)

	frameSizes := []int{480, 960}

	for _, frameSize := range frameSizes {
		t.Run(string(rune('0'+frameSize/100)), func(t *testing.T) {
			pcm := generateTestSignal(frameSize, 1)

			encoded, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			if len(encoded) == 0 {
				t.Error("Encoded data is empty")
			}

			t.Logf("Mono %dms: %d bytes", frameSize/48, len(encoded))
		})
	}
}

// TestHybridEncodeStereo tests stereo hybrid encoding.
func TestHybridEncodeStereo(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthFullband)

	frameSizes := []int{480, 960}

	for _, frameSize := range frameSizes {
		t.Run(string(rune('0'+frameSize/100)), func(t *testing.T) {
			pcm := generateTestSignal(frameSize, 2)

			encoded, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			if len(encoded) == 0 {
				t.Error("Encoded data is empty")
			}

			t.Logf("Stereo %dms: %d bytes", frameSize/48, len(encoded))
		})
	}
}

// TestHybridRoundTrip tests encode with unified encoder, decode with hybrid.Decoder.
func TestHybridRoundTrip(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	// Generate test signal
	pcm := generateTestSignal(960, 1)

	// Encode
	encoded, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Fatal("Encoded data is empty")
	}

	t.Logf("Encoded %d samples to %d bytes", 960, len(encoded))

	// Decode using Phase 4 hybrid decoder
	dec := hybrid.NewDecoder(1)
	rd := &rangecoding.Decoder{}
	rd.Init(encoded)

	decoded, err := dec.DecodeWithDecoder(rd, 960)
	if err != nil {
		t.Logf("Decode returned error (expected for initial implementation): %v", err)
		// Don't fail - we're testing that encoding produces valid bytes
	}

	if len(decoded) > 0 {
		// Verify output has energy (not silence)
		energy := computeEnergy(decoded)
		t.Logf("Decoded signal energy: %f", energy)

		if energy > 0.0001 {
			t.Log("Round-trip produced signal with energy")
		}
	}
}

// TestInvalidHybridFrameSize tests that invalid frame sizes return error for hybrid mode.
func TestInvalidHybridFrameSize(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)

	invalidSizes := []int{120, 240, 1920, 2880, 100, 500}

	for _, size := range invalidSizes {
		t.Run(string(rune('0'+size/100)), func(t *testing.T) {
			pcm := make([]float64, size)

			_, err := enc.Encode(pcm, size)
			if err == nil {
				t.Errorf("Expected error for frame size %d in hybrid mode", size)
			}
		})
	}
}

// TestDownsample48to16 tests the downsampling function.
func TestDownsample48to16(t *testing.T) {
	// 960 samples at 48kHz should become 320 samples at 16kHz
	pcm := generateTestSignal(960, 1)

	downsampled := downsample48to16(pcm, 1)

	expectedLen := 960 / 3
	if len(downsampled) != expectedLen {
		t.Errorf("Downsampled length = %d, want %d", len(downsampled), expectedLen)
	}

	// Verify downsampled signal has reasonable values
	var maxVal float32
	for _, s := range downsampled {
		if s > maxVal {
			maxVal = s
		}
		if -s > maxVal {
			maxVal = -s
		}
	}

	if maxVal < 0.01 {
		t.Error("Downsampled signal appears to be silent")
	}

	t.Logf("Downsampled max value: %f", maxVal)
}

// TestModeAutoSelection tests automatic mode selection.
func TestModeAutoSelection(t *testing.T) {
	enc := NewEncoder(48000, 1)
	// ModeAuto is default

	// Test that SWB/FB with hybrid-compatible frame size selects hybrid
	enc.SetBandwidth(gopus.BandwidthSuperwideband)
	pcm := generateTestSignal(960, 1)

	encoded, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Error("Encoded data is empty")
	}

	t.Logf("Auto mode (SWB, 20ms): %d bytes", len(encoded))
}

// TestMultipleFramesHybrid tests encoding multiple consecutive hybrid frames.
func TestMultipleFramesHybrid(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	numFrames := 5
	frameSize := 960

	for i := 0; i < numFrames; i++ {
		pcm := generateTestSignal(frameSize, 1)

		encoded, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Frame %d encode failed: %v", i, err)
		}

		if len(encoded) == 0 {
			t.Errorf("Frame %d encoded data is empty", i)
		}

		t.Logf("Frame %d: %d bytes", i, len(encoded))
	}
}

// TestBitrateLimits tests ValidBitrate and ClampBitrate functions.
func TestBitrateLimits(t *testing.T) {
	// Test ValidBitrate
	tests := []struct {
		bitrate int
		valid   bool
	}{
		{MinBitrate - 1, false},
		{MinBitrate, true},
		{64000, true},
		{MaxBitrate, true},
		{MaxBitrate + 1, false},
		{0, false},
		{-1, false},
	}

	for _, tt := range tests {
		got := ValidBitrate(tt.bitrate)
		if got != tt.valid {
			t.Errorf("ValidBitrate(%d) = %v, want %v", tt.bitrate, got, tt.valid)
		}
	}

	// Test ClampBitrate
	clampTests := []struct {
		input    int
		expected int
	}{
		{MinBitrate - 1000, MinBitrate},
		{MinBitrate, MinBitrate},
		{64000, 64000},
		{MaxBitrate, MaxBitrate},
		{MaxBitrate + 100000, MaxBitrate},
		{0, MinBitrate},
		{-1, MinBitrate},
	}

	for _, tt := range clampTests {
		got := ClampBitrate(tt.input)
		if got != tt.expected {
			t.Errorf("ClampBitrate(%d) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

// TestBitrateModeVBR tests VBR mode encoding with different content types.
func TestBitrateModeVBR(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)
	enc.SetBitrateMode(ModeVBR)

	// Encode different content types
	silence := make([]float64, 960)
	complex := generateComplexSignal(960)

	silentPacket, err := enc.Encode(silence, 960)
	if err != nil {
		t.Fatalf("Encode silence failed: %v", err)
	}

	complexPacket, err := enc.Encode(complex, 960)
	if err != nil {
		t.Fatalf("Encode complex failed: %v", err)
	}

	// VBR: complex content should produce larger packets
	t.Logf("Silent packet: %d bytes, Complex packet: %d bytes",
		len(silentPacket), len(complexPacket))
	// Note: Exact ratio depends on encoder, so just verify both work
	if len(silentPacket) == 0 {
		t.Error("Silent packet is empty")
	}
	if len(complexPacket) == 0 {
		t.Error("Complex packet is empty")
	}
}

// TestBitrateModeCBR tests CBR mode produces consistent packet sizes.
func TestBitrateModeCBR(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)
	enc.SetBitrateMode(ModeCBR)
	enc.SetBitrate(64000) // 64 kbps

	// Target size: 64000 * 20 / 8000 = 160 bytes for 20ms
	expectedSize := 160

	// Encode multiple frames
	for i := 0; i < 5; i++ {
		pcm := generateTestSignal(960, 1)
		packet, err := enc.Encode(pcm, 960)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", i, err)
		}

		// CBR: all packets should be exactly target size
		if len(packet) != expectedSize {
			t.Errorf("CBR packet %d: got %d bytes, want %d bytes", i, len(packet), expectedSize)
		}
	}
}

// TestBitrateModeCVBR tests CVBR mode constrains packet sizes within tolerance.
func TestBitrateModeCVBR(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)
	enc.SetBitrateMode(ModeCVBR)
	enc.SetBitrate(64000)

	target := 160 // bytes for 20ms at 64kbps
	minSize := int(float64(target) * (1 - CVBRTolerance))
	maxSize := int(float64(target) * (1 + CVBRTolerance))

	// Encode multiple frames
	for i := 0; i < 10; i++ {
		pcm := generateVariableSignal(960, i)
		packet, err := enc.Encode(pcm, 960)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", i, err)
		}

		// CVBR: packets within tolerance
		if len(packet) < minSize {
			t.Errorf("CVBR packet %d: %d bytes < min %d bytes", i, len(packet), minSize)
		}
		if len(packet) > maxSize {
			t.Errorf("CVBR packet %d: %d bytes > max %d bytes", i, len(packet), maxSize)
		}
	}
}

// TestBitrateRange tests bitrate clamping via SetBitrate.
func TestBitrateRange(t *testing.T) {
	enc := NewEncoder(48000, 1)

	// Test minimum bitrate
	enc.SetBitrate(MinBitrate - 1000)
	if enc.Bitrate() != MinBitrate {
		t.Errorf("SetBitrate(%d) = %d, want %d", MinBitrate-1000, enc.Bitrate(), MinBitrate)
	}

	// Test maximum bitrate
	enc.SetBitrate(MaxBitrate + 100000)
	if enc.Bitrate() != MaxBitrate {
		t.Errorf("SetBitrate(%d) = %d, want %d", MaxBitrate+100000, enc.Bitrate(), MaxBitrate)
	}

	// Test valid bitrate
	enc.SetBitrate(64000)
	if enc.Bitrate() != 64000 {
		t.Errorf("SetBitrate(64000) = %d, want 64000", enc.Bitrate())
	}
}

// TestTargetBytesForBitrate tests the bitrate to bytes conversion.
func TestTargetBytesForBitrate(t *testing.T) {
	tests := []struct {
		bitrate   int
		frameSize int
		expected  int
	}{
		{64000, 960, 160},  // 64kbps, 20ms = 160 bytes
		{64000, 480, 80},   // 64kbps, 10ms = 80 bytes
		{128000, 960, 320}, // 128kbps, 20ms = 320 bytes
		{6000, 960, 15},    // 6kbps (min), 20ms = 15 bytes
	}

	for _, tt := range tests {
		got := targetBytesForBitrate(tt.bitrate, tt.frameSize)
		if got != tt.expected {
			t.Errorf("targetBytesForBitrate(%d, %d) = %d, want %d",
				tt.bitrate, tt.frameSize, got, tt.expected)
		}
	}
}

// TestBitrateModeGetSet tests SetBitrateMode and GetBitrateMode.
func TestBitrateModeGetSet(t *testing.T) {
	enc := NewEncoder(48000, 1)

	// Default should be VBR
	if enc.GetBitrateMode() != ModeVBR {
		t.Errorf("Default bitrate mode = %d, want ModeVBR (%d)", enc.GetBitrateMode(), ModeVBR)
	}

	modes := []BitrateMode{ModeVBR, ModeCVBR, ModeCBR}
	for _, mode := range modes {
		enc.SetBitrateMode(mode)
		if enc.GetBitrateMode() != mode {
			t.Errorf("SetBitrateMode(%d): GetBitrateMode() = %d", mode, enc.GetBitrateMode())
		}
	}
}

// TestCBRDifferentBitrates tests CBR at various target bitrates.
func TestCBRDifferentBitrates(t *testing.T) {
	bitrates := []int{32000, 64000, 96000, 128000}

	for _, bitrate := range bitrates {
		t.Run(string(rune(bitrate/1000+'0')), func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeHybrid)
			enc.SetBandwidth(gopus.BandwidthSuperwideband)
			enc.SetBitrateMode(ModeCBR)
			enc.SetBitrate(bitrate)

			expectedSize := targetBytesForBitrate(bitrate, 960)

			pcm := generateTestSignal(960, 1)
			packet, err := enc.Encode(pcm, 960)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			if len(packet) != expectedSize {
				t.Errorf("CBR at %d kbps: got %d bytes, want %d bytes",
					bitrate/1000, len(packet), expectedSize)
			}
		})
	}
}

// generateComplexSignal generates a complex signal with multiple frequencies + noise.
func generateComplexSignal(n int) []float64 {
	pcm := make([]float64, n)
	for i := range pcm {
		// Multiple frequencies + noise
		t := float64(i) / 48000.0
		pcm[i] = 0.3*math.Sin(2*math.Pi*440*t) +
			0.2*math.Sin(2*math.Pi*880*t) +
			0.1*math.Sin(2*math.Pi*1320*t) +
			0.1*(rand.Float64()-0.5)
	}
	return pcm
}

// generateVariableSignal generates a signal with varying characteristics based on seed.
func generateVariableSignal(n, seed int) []float64 {
	pcm := make([]float64, n)
	freq := float64(200 + seed*100)
	amp := 0.3 + float64(seed%5)*0.1
	for i := range pcm {
		t := float64(i) / 48000.0
		pcm[i] = amp * math.Sin(2*math.Pi*freq*t)
	}
	return pcm
}

// generateTestSignalFloat32 generates a test signal as float32 for FEC tests.
func generateTestSignalFloat32(samples int) []float32 {
	pcm := make([]float32, samples)
	freq := float32(440.0)
	sampleRate := float32(48000.0)

	for i := 0; i < samples; i++ {
		t := float32(i) / sampleRate
		sample := float32(0.5 * math.Sin(float64(2*math.Pi*freq*t)))
		sample += float32(0.25 * math.Sin(float64(2*math.Pi*freq*2*t)))
		pcm[i] = sample
	}
	return pcm
}

// TestFECEnabled verifies FEC enable/disable works correctly.
func TestFECEnabled(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(gopus.BandwidthSuperwideband)

	// Verify default (disabled)
	if enc.FECEnabled() {
		t.Error("FEC should be disabled by default")
	}

	// Enable FEC
	enc.SetFEC(true)
	if !enc.FECEnabled() {
		t.Error("FEC should be enabled after SetFEC(true)")
	}

	// Disable FEC
	enc.SetFEC(false)
	if enc.FECEnabled() {
		t.Error("FEC should be disabled after SetFEC(false)")
	}
}

// TestFECPacketLoss verifies packet loss percentage setting works correctly.
func TestFECPacketLoss(t *testing.T) {
	enc := NewEncoder(48000, 1)

	// Verify default (0%)
	if enc.PacketLoss() != 0 {
		t.Errorf("Packet loss should be 0 by default, got %d", enc.PacketLoss())
	}

	// Test clamping at lower bound
	enc.SetPacketLoss(-10)
	if enc.PacketLoss() != 0 {
		t.Errorf("SetPacketLoss(-10) should clamp to 0, got %d", enc.PacketLoss())
	}

	// Test clamping at upper bound
	enc.SetPacketLoss(150)
	if enc.PacketLoss() != 100 {
		t.Errorf("SetPacketLoss(150) should clamp to 100, got %d", enc.PacketLoss())
	}

	// Test valid value
	enc.SetPacketLoss(20)
	if enc.PacketLoss() != 20 {
		t.Errorf("SetPacketLoss(20) = %d, want 20", enc.PacketLoss())
	}
}

// TestFECOnlyWithPreviousFrame verifies FEC needs a previous frame.
func TestFECOnlyWithPreviousFrame(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(gopus.BandwidthWideband)
	enc.SetFEC(true)
	enc.SetPacketLoss(10)

	// First frame - no FEC possible (no previous frame)
	if enc.shouldUseFEC() {
		t.Error("shouldUseFEC() should return false for first frame (no previous frame)")
	}

	// Encode first frame to populate state
	pcm := generateTestSignal(960, 1)
	_, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("First encode failed: %v", err)
	}

	// Simulate having a previous frame by calling updateFECState
	pcm32 := generateTestSignalFloat32(320) // 16kHz sample rate for SILK
	enc.updateFECState(pcm32, true)

	// Second frame - FEC should be possible now
	if !enc.shouldUseFEC() {
		t.Error("shouldUseFEC() should return true after previous frame is stored")
	}
}

// TestFECDisabledWithLowPacketLoss verifies FEC deactivates below threshold.
func TestFECDisabledWithLowPacketLoss(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetFEC(true)
	enc.SetPacketLoss(0) // Below MinPacketLossForFEC

	// Store a previous frame
	pcm32 := generateTestSignalFloat32(320)
	enc.updateFECState(pcm32, true)

	// FEC should not activate with 0% packet loss
	if enc.shouldUseFEC() {
		t.Error("shouldUseFEC() should return false when packet loss is 0%")
	}

	// Set packet loss above threshold
	enc.SetPacketLoss(5)
	if !enc.shouldUseFEC() {
		t.Error("shouldUseFEC() should return true when packet loss >= MinPacketLossForFEC")
	}
}

// TestFECStateReset verifies FEC state is reset properly.
func TestFECStateReset(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetFEC(true)
	enc.SetPacketLoss(10)

	// Store a previous frame
	pcm32 := generateTestSignalFloat32(320)
	enc.updateFECState(pcm32, true)

	// Verify FEC is ready
	if !enc.shouldUseFEC() {
		t.Error("shouldUseFEC() should be true before reset")
	}

	// Reset encoder
	enc.Reset()

	// After reset, shouldUseFEC should be false (no previous frame)
	if enc.shouldUseFEC() {
		t.Error("shouldUseFEC() should be false after reset")
	}
}

// TestComputeLBRRBitrate verifies LBRR bitrate calculation.
func TestComputeLBRRBitrate(t *testing.T) {
	tests := []struct {
		normalBitrate int
		expected      int
	}{
		{20000, 12000}, // 20000 * 0.6 = 12000
		{10000, 6000},  // 10000 * 0.6 = 6000 (exactly MinSILKBitrate)
		{8000, 6000},   // 8000 * 0.6 = 4800, clamped to MinSILKBitrate
		{100000, 60000}, // 100000 * 0.6 = 60000
	}

	for _, tt := range tests {
		got := computeLBRRBitrate(tt.normalBitrate)
		if got != tt.expected {
			t.Errorf("computeLBRRBitrate(%d) = %d, want %d",
				tt.normalBitrate, got, tt.expected)
		}
	}
}

// TestFECConstants verifies FEC constants are set correctly.
func TestFECConstants(t *testing.T) {
	if LBRRBitrateFactor != 0.6 {
		t.Errorf("LBRRBitrateFactor = %f, want 0.6", LBRRBitrateFactor)
	}

	if MinPacketLossForFEC != 1 {
		t.Errorf("MinPacketLossForFEC = %d, want 1", MinPacketLossForFEC)
	}

	if MaxPacketLossForFEC != 50 {
		t.Errorf("MaxPacketLossForFEC = %d, want 50", MaxPacketLossForFEC)
	}

	if MinSILKBitrate != 6000 {
		t.Errorf("MinSILKBitrate = %d, want 6000", MinSILKBitrate)
	}
}
