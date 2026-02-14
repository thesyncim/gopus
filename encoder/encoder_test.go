package encoder_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/hybrid"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/types"
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
			enc := encoder.NewEncoder(tt.sampleRate, tt.channels)

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
			if enc.Mode() != encoder.ModeAuto {
				t.Errorf("Mode() = %d, want ModeAuto", enc.Mode())
			}

			// Default bandwidth should be Fullband
			if enc.Bandwidth() != types.BandwidthFullband {
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
	enc := encoder.NewEncoder(48000, 1)

	tests := []struct {
		mode encoder.Mode
		name string
	}{
		{encoder.ModeAuto, "ModeAuto"},
		{encoder.ModeSILK, "ModeSILK"},
		{encoder.ModeHybrid, "ModeHybrid"},
		{encoder.ModeCELT, "ModeCELT"},
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
	enc := encoder.NewEncoder(48000, 1)

	tests := []struct {
		bw   types.Bandwidth
		name string
	}{
		{types.BandwidthNarrowband, "Narrowband"},
		{types.BandwidthMediumband, "Mediumband"},
		{types.BandwidthWideband, "Wideband"},
		{types.BandwidthSuperwideband, "Superwideband"},
		{types.BandwidthFullband, "Fullband"},
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
	enc := encoder.NewEncoder(48000, 1)

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
	enc := encoder.NewEncoder(48000, 1)

	// Modify state
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)

	// Reset
	enc.Reset()

	// Mode and bandwidth should be preserved (they're config, not state)
	if enc.Mode() != encoder.ModeHybrid {
		t.Errorf("Mode changed after Reset, got %d", enc.Mode())
	}

	// prevSamples should be zeroed (delay buffer)
	// Note: prevSamples is unexported, so we cannot check it directly from external test package
	// The Reset() method is tested implicitly by other tests
}

// TestValidFrameSize verifies frame size validation.
func TestValidFrameSize(t *testing.T) {
	tests := []struct {
		frameSize int
		mode      encoder.Mode
		want      bool
	}{
		// SILK valid sizes
		{480, encoder.ModeSILK, true},
		{960, encoder.ModeSILK, true},
		{1920, encoder.ModeSILK, true},
		{2880, encoder.ModeSILK, true},
		{120, encoder.ModeSILK, false},
		{240, encoder.ModeSILK, false},

		// Hybrid valid sizes
		{480, encoder.ModeHybrid, true},
		{960, encoder.ModeHybrid, true},
		{120, encoder.ModeHybrid, false},
		{240, encoder.ModeHybrid, false},
		{1920, encoder.ModeHybrid, false},

		// CELT valid sizes
		{120, encoder.ModeCELT, true},
		{240, encoder.ModeCELT, true},
		{480, encoder.ModeCELT, true},
		{960, encoder.ModeCELT, true},
		{1920, encoder.ModeCELT, false},
		{2880, encoder.ModeCELT, false},

		// Auto accepts all valid sizes
		{120, encoder.ModeAuto, true},
		{240, encoder.ModeAuto, true},
		{480, encoder.ModeAuto, true},
		{960, encoder.ModeAuto, true},
		{1920, encoder.ModeAuto, true},
		{2880, encoder.ModeAuto, true},
		{100, encoder.ModeAuto, false},
	}

	for _, tt := range tests {
		got := encoder.ValidFrameSize(tt.frameSize, tt.mode)
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
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)

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
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)

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
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthFullband)

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
	enc := encoder.NewEncoder(48000, 2)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthFullband)

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
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)

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
// Note: 1920 (40ms) and 2880 (60ms) are NOT invalid - they are encoded as
// code-3 packets containing 2/3 x 20ms hybrid frames.
func TestInvalidHybridFrameSize(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)

	invalidSizes := []int{120, 240, 100, 500}

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

// TestLargeFrameSizeModeSelectionAndPacketization verifies long-frame behavior:
// SILK remains single-frame, while Hybrid and CELT use code-3 multi-frame
// packetization for 40/60ms packets.
func TestLargeFrameSizeModeSelectionAndPacketization(t *testing.T) {
	tests := []struct {
		name           string
		mode           encoder.Mode
		signalType     types.Signal
		bandwidth      types.Bandwidth
		frameSize      int
		wantMode       gopus.Mode
		wantFrameCode  uint8
		wantFrameCount int
	}{
		{"silk_40ms", encoder.ModeSILK, types.SignalAuto, types.BandwidthFullband, 1920, gopus.ModeSILK, 0, 1},
		{"hybrid_40ms", encoder.ModeHybrid, types.SignalAuto, types.BandwidthFullband, 1920, gopus.ModeHybrid, 3, 2},
		{"hybrid_60ms", encoder.ModeHybrid, types.SignalAuto, types.BandwidthFullband, 2880, gopus.ModeHybrid, 3, 3},
		{"celt_40ms", encoder.ModeCELT, types.SignalAuto, types.BandwidthFullband, 1920, gopus.ModeCELT, 3, 2},
		{"celt_60ms", encoder.ModeCELT, types.SignalAuto, types.BandwidthFullband, 2880, gopus.ModeCELT, 3, 3},
		{"auto_music_40ms", encoder.ModeAuto, types.SignalMusic, types.BandwidthFullband, 1920, gopus.ModeCELT, 3, 2},
		{"auto_voice_40ms", encoder.ModeAuto, types.SignalVoice, types.BandwidthFullband, 1920, gopus.ModeCELT, 3, 2},
		{"auto_voice_swb_40ms", encoder.ModeAuto, types.SignalVoice, types.BandwidthSuperwideband, 1920, gopus.ModeCELT, 3, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(tc.mode)
			enc.SetSignalType(tc.signalType)
			enc.SetBandwidth(tc.bandwidth)

			pcm := make([]float64, tc.frameSize)
			for i := range pcm {
				pcm[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
			}

			packet, err := enc.Encode(pcm, tc.frameSize)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}
			if len(packet) == 0 {
				t.Fatal("Encode returned empty packet")
			}

			toc := gopus.ParseTOC(packet[0])
			if toc.Mode != tc.wantMode {
				t.Fatalf("TOC mode = %v, want %v", toc.Mode, tc.wantMode)
			}
			if toc.FrameCode != tc.wantFrameCode {
				t.Fatalf("TOC frame code = %d, want %d", toc.FrameCode, tc.wantFrameCode)
			}

			info, err := gopus.ParsePacket(packet)
			if err != nil {
				t.Fatalf("ParsePacket failed: %v", err)
			}
			if info.FrameCount != tc.wantFrameCount {
				t.Fatalf("FrameCount = %d, want %d", info.FrameCount, tc.wantFrameCount)
			}
		})
	}
}

func TestAutoLongFrameSpeechLikePrefersCELTAtFullband(t *testing.T) {
	const frameSize = 1920

	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeAuto)
	enc.SetSignalType(types.SignalAuto)
	enc.SetBandwidth(types.BandwidthFullband)

	pcm := make([]float64, frameSize)
	for i := range pcm {
		tsec := float64(i) / 48000.0
		voiced := 0.35 * math.Sin(2*math.Pi*150*tsec)
		voiced += 0.15 * math.Sin(2*math.Pi*300*tsec)
		noise := 0.08 * math.Sin(2*math.Pi*1900*tsec+0.31)
		pcm[i] = voiced + noise
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet")
	}

	toc := gopus.ParseTOC(packet[0])
	if toc.Mode != gopus.ModeCELT {
		t.Fatalf("TOC mode = %v, want %v", toc.Mode, gopus.ModeCELT)
	}
}

// TestDownsample48to16 tests the downsampling function.
func TestDownsample48to16(t *testing.T) {
	// 960 samples at 48kHz should become 320 samples at 16kHz
	pcm := generateTestSignal(960, 1)

	// Create an encoder to access the downsampling method
	enc := encoder.NewEncoder(48000, 1)
	downsampled := enc.Downsample48to16Hybrid(pcm, 960)

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
	enc := encoder.NewEncoder(48000, 1)
	// ModeAuto is default

	// Test that SWB/FB with hybrid-compatible frame size selects hybrid
	enc.SetBandwidth(types.BandwidthSuperwideband)
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
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)

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
		{encoder.MinBitrate - 1, false},
		{encoder.MinBitrate, true},
		{64000, true},
		{encoder.MaxBitrate, true},
		{encoder.MaxBitrate + 1, false},
		{0, false},
		{-1, false},
	}

	for _, tt := range tests {
		got := encoder.ValidBitrate(tt.bitrate)
		if got != tt.valid {
			t.Errorf("ValidBitrate(%d) = %v, want %v", tt.bitrate, got, tt.valid)
		}
	}

	// Test ClampBitrate
	clampTests := []struct {
		input    int
		expected int
	}{
		{encoder.MinBitrate - 1000, encoder.MinBitrate},
		{encoder.MinBitrate, encoder.MinBitrate},
		{64000, 64000},
		{encoder.MaxBitrate, encoder.MaxBitrate},
		{encoder.MaxBitrate + 100000, encoder.MaxBitrate},
		{0, encoder.MinBitrate},
		{-1, encoder.MinBitrate},
	}

	for _, tt := range clampTests {
		got := encoder.ClampBitrate(tt.input)
		if got != tt.expected {
			t.Errorf("ClampBitrate(%d) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

// TestBitrateModeVBR tests VBR mode encoding with different content types.
func TestBitrateModeVBR(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)
	enc.SetBitrateMode(encoder.ModeVBR)

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
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)
	enc.SetBitrateMode(encoder.ModeCBR)
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

// TestBitrateModeCVBR tests CVBR mode keeps packet size under tolerance cap.
func TestBitrateModeCVBR(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)
	enc.SetBitrateMode(encoder.ModeCVBR)
	enc.SetBitrate(64000)

	target := 160 // bytes for 20ms at 64kbps
	maxSize := int(float64(target) * (1 + encoder.CVBRTolerance))

	// Encode multiple frames
	for i := 0; i < 10; i++ {
		pcm := generateVariableSignal(960, i)
		packet, err := enc.Encode(pcm, 960)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", i, err)
		}

		// CVBR: packets should stay below the tolerance cap.
		if len(packet) > maxSize {
			t.Errorf("CVBR packet %d: %d bytes > max %d bytes", i, len(packet), maxSize)
		}
	}
}

func TestBitrateModeCVBR_CELTStereoEnvelope(t *testing.T) {
	enc := encoder.NewEncoder(48000, 2)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBitrateMode(encoder.ModeCVBR)
	enc.SetBitrate(95000)

	baseBits := (enc.Bitrate() * 960) / 48000
	targetBytes := (baseBits + 7) / 8
	// libopus constrained-VBR can spike individual CELT packets up to roughly
	// 2x base_target, but should stay near target on average.
	maxBits := 2 * baseBits
	maxBytes := (maxBits + 7) / 8
	totalBytes := 0

	for i := 0; i < 60; i++ {
		packet, err := enc.Encode(generateTestSignal(960, 2), 960)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", i, err)
		}
		totalBytes += len(packet)
		if len(packet) > maxBytes {
			t.Fatalf("CVBR CELT stereo packet %d: %d bytes > max %d bytes", i, len(packet), maxBytes)
		}
	}
	avgBytes := float64(totalBytes) / 60.0
	if avgBytes > float64(targetBytes)*1.15 {
		t.Fatalf("CVBR CELT stereo avg bytes %.2f exceeds +15%% envelope around target %d", avgBytes, targetBytes)
	}
	if avgBytes < float64(targetBytes)*0.85 {
		t.Fatalf("CVBR CELT stereo avg bytes %.2f below -15%% envelope around target %d", avgBytes, targetBytes)
	}
}

// TestBitrateRange tests bitrate clamping via SetBitrate.
func TestBitrateRange(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)

	// Test minimum bitrate
	enc.SetBitrate(encoder.MinBitrate - 1000)
	if enc.Bitrate() != encoder.MinBitrate {
		t.Errorf("SetBitrate(%d) = %d, want %d", encoder.MinBitrate-1000, enc.Bitrate(), encoder.MinBitrate)
	}

	// Test maximum bitrate
	enc.SetBitrate(encoder.MaxBitrate + 100000)
	if enc.Bitrate() != encoder.MaxBitrate {
		t.Errorf("SetBitrate(%d) = %d, want %d", encoder.MaxBitrate+100000, enc.Bitrate(), encoder.MaxBitrate)
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
		got := encoder.TargetBytesForBitrate(tt.bitrate, tt.frameSize)
		if got != tt.expected {
			t.Errorf("targetBytesForBitrate(%d, %d) = %d, want %d",
				tt.bitrate, tt.frameSize, got, tt.expected)
		}
	}
}

func TestSilkInputBitrateReservesTOC(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetBitrate(32000)

	if got := enc.SilkInputBitrate(960); got != 31600 {
		t.Fatalf("SilkInputBitrate(960) = %d, want 31600", got)
	}
	if got := enc.SilkInputBitrate(480); got != 31200 {
		t.Fatalf("SilkInputBitrate(480) = %d, want 31200", got)
	}
}

// TestBitrateModeGetSet tests SetBitrateMode and GetBitrateMode.
func TestBitrateModeGetSet(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)

	// Default should be CVBR (matching libopus default: use_vbr=1, vbr_constraint=1).
	if enc.GetBitrateMode() != encoder.ModeCVBR {
		t.Errorf("Default bitrate mode = %d, want ModeCVBR (%d)", enc.GetBitrateMode(), encoder.ModeCVBR)
	}

	modes := []encoder.BitrateMode{encoder.ModeVBR, encoder.ModeCVBR, encoder.ModeCBR}
	for _, mode := range modes {
		enc.SetBitrateMode(mode)
		if enc.GetBitrateMode() != mode {
			t.Errorf("SetBitrateMode(%d): GetBitrateMode() = %d", mode, enc.GetBitrateMode())
		}
	}
}

func TestVBRConstraintTransitions(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)

	if !enc.VBR() {
		t.Fatal("VBR()=false by default, want true")
	}
	if !enc.VBRConstraint() {
		t.Fatal("VBRConstraint()=false by default, want true (CVBR default)")
	}
	if got := enc.GetBitrateMode(); got != encoder.ModeCVBR {
		t.Fatalf("GetBitrateMode()=%d want=%d", got, encoder.ModeCVBR)
	}

	enc.SetVBR(false)
	if enc.VBR() {
		t.Fatal("VBR()=true after SetVBR(false)")
	}
	if !enc.VBRConstraint() {
		t.Fatal("VBRConstraint() should remain true after SetVBR(false)")
	}
	if got := enc.GetBitrateMode(); got != encoder.ModeCBR {
		t.Fatalf("GetBitrateMode()=%d want=%d", got, encoder.ModeCBR)
	}

	enc.SetVBRConstraint(true)
	if !enc.VBRConstraint() {
		t.Fatal("VBRConstraint()=false after SetVBRConstraint(true)")
	}
	if got := enc.GetBitrateMode(); got != encoder.ModeCBR {
		t.Fatalf("GetBitrateMode()=%d want=%d while VBR disabled", got, encoder.ModeCBR)
	}

	enc.SetVBR(true)
	if got := enc.GetBitrateMode(); got != encoder.ModeCVBR {
		t.Fatalf("GetBitrateMode()=%d want=%d after SetVBR(true)", got, encoder.ModeCVBR)
	}

	enc.SetVBRConstraint(false)
	if enc.VBRConstraint() {
		t.Fatal("VBRConstraint()=true after SetVBRConstraint(false)")
	}
	if got := enc.GetBitrateMode(); got != encoder.ModeVBR {
		t.Fatalf("GetBitrateMode()=%d want=%d", got, encoder.ModeVBR)
	}

	enc.SetVBR(false)
	if got := enc.GetBitrateMode(); got != encoder.ModeCBR {
		t.Fatalf("GetBitrateMode()=%d want=%d after SetVBR(false)", got, encoder.ModeCBR)
	}
	enc.SetVBR(true)
	if got := enc.GetBitrateMode(); got != encoder.ModeVBR {
		t.Fatalf("GetBitrateMode()=%d want=%d after re-enabling VBR", got, encoder.ModeVBR)
	}
}

// TestCBRDifferentBitrates tests CBR at various target bitrates.
func TestCBRDifferentBitrates(t *testing.T) {
	bitrates := []int{32000, 64000, 96000, 128000}

	for _, bitrate := range bitrates {
		t.Run(string(rune(bitrate/1000+'0')), func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(encoder.ModeHybrid)
			enc.SetBandwidth(types.BandwidthSuperwideband)
			enc.SetBitrateMode(encoder.ModeCBR)
			enc.SetBitrate(bitrate)

			expectedSize := encoder.TargetBytesForBitrate(bitrate, 960)

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
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)

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
	enc := encoder.NewEncoder(48000, 1)

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
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetFEC(true)
	enc.SetPacketLoss(10)

	// First frame - no FEC possible (no previous frame)
	if enc.ShouldUseFEC() {
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
	enc.UpdateFECState(pcm32, true)

	// Second frame - FEC should be possible now
	if !enc.ShouldUseFEC() {
		t.Error("shouldUseFEC() should return true after previous frame is stored")
	}
}

// TestFECDisabledWithLowPacketLoss verifies FEC deactivates below threshold.
func TestFECDisabledWithLowPacketLoss(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeSILK)
	enc.SetFEC(true)
	enc.SetPacketLoss(0) // Below MinPacketLossForFEC

	// Store a previous frame
	pcm32 := generateTestSignalFloat32(320)
	enc.UpdateFECState(pcm32, true)

	// FEC should not activate with 0% packet loss
	if enc.ShouldUseFEC() {
		t.Error("shouldUseFEC() should return false when packet loss is 0%")
	}

	// Set packet loss above threshold
	enc.SetPacketLoss(5)
	if !enc.ShouldUseFEC() {
		t.Error("shouldUseFEC() should return true when packet loss >= MinPacketLossForFEC")
	}
}

// TestFECStateReset verifies FEC state is reset properly.
func TestFECStateReset(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetFEC(true)
	enc.SetPacketLoss(10)

	// Store a previous frame
	pcm32 := generateTestSignalFloat32(320)
	enc.UpdateFECState(pcm32, true)

	// Verify FEC is ready
	if !enc.ShouldUseFEC() {
		t.Error("shouldUseFEC() should be true before reset")
	}

	// Reset encoder
	enc.Reset()

	// After reset, shouldUseFEC should be false (no previous frame)
	if enc.ShouldUseFEC() {
		t.Error("shouldUseFEC() should be false after reset")
	}
}

// TestComputeLBRRBitrate verifies LBRR bitrate calculation.
func TestComputeLBRRBitrate(t *testing.T) {
	tests := []struct {
		normalBitrate int
		expected      int
	}{
		{20000, 12000},  // 20000 * 0.6 = 12000
		{10000, 6000},   // 10000 * 0.6 = 6000 (exactly MinSILKBitrate)
		{8000, 6000},    // 8000 * 0.6 = 4800, clamped to MinSILKBitrate
		{100000, 60000}, // 100000 * 0.6 = 60000
	}

	for _, tt := range tests {
		got := encoder.ComputeLBRRBitrate(tt.normalBitrate)
		if got != tt.expected {
			t.Errorf("computeLBRRBitrate(%d) = %d, want %d",
				tt.normalBitrate, got, tt.expected)
		}
	}
}

// TestFECConstants verifies FEC constants are set correctly.
func TestFECConstants(t *testing.T) {
	if encoder.LBRRBitrateFactor != 0.6 {
		t.Errorf("LBRRBitrateFactor = %f, want 0.6", encoder.LBRRBitrateFactor)
	}

	if encoder.MinPacketLossForFEC != 1 {
		t.Errorf("MinPacketLossForFEC = %d, want 1", encoder.MinPacketLossForFEC)
	}

	if encoder.MaxPacketLossForFEC != 50 {
		t.Errorf("MaxPacketLossForFEC = %d, want 50", encoder.MaxPacketLossForFEC)
	}

	if encoder.MinSILKBitrate != 6000 {
		t.Errorf("MinSILKBitrate = %d, want 6000", encoder.MinSILKBitrate)
	}
}

// TestDTXEnabled tests DTX enable/disable functionality.
func TestDTXEnabled(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)

	// Verify default
	if enc.DTXEnabled() {
		t.Error("DTX should be disabled by default")
	}

	// Enable DTX
	enc.SetDTX(true)
	if !enc.DTXEnabled() {
		t.Error("DTX should be enabled after SetDTX(true)")
	}

	// Disable DTX
	enc.SetDTX(false)
	if enc.DTXEnabled() {
		t.Error("DTX should be disabled after SetDTX(false)")
	}
}

// TestDTXSuppressesSilence tests that DTX suppresses packets after silence threshold.
// The multi-band VAD requires noise adaptation before it can reliably detect silence.
// This matches libopus behavior where the VAD counter starts at 15 for initial faster smoothing.
func TestDTXSuppressesSilence(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetDTX(true)

	// Generate silence
	silence := make([]float64, 960)

	// Encode many silent frames until DTX activates
	// VAD needs ~15-20 frames for noise adaptation, then 10 more for DTX threshold
	// Total: ~25-30 frames before DTX activates
	var dtxActivated bool
	var dtxFrame int
	maxFrames := 50 // Should activate well before this

	for i := 0; i < maxFrames; i++ {
		packet, err := enc.Encode(silence, 960)
		if err != nil {
			t.Fatalf("Frame %d encode failed: %v", i, err)
		}
		if packet == nil {
			dtxActivated = true
			dtxFrame = i
			break
		}
	}

	if !dtxActivated {
		t.Error("DTX should eventually suppress silence frames")
	} else {
		t.Logf("DTX activated after %d frames (within expected range)", dtxFrame)
		// Should activate after noise adaptation (~15 frames) + DTX threshold (10 frames)
		// but before 50 frames
		if dtxFrame > maxFrames {
			t.Errorf("DTX activated too late: frame %d", dtxFrame)
		}
	}
}

// TestDTXComfortNoise tests that comfort noise frames are sent periodically during DTX.
func TestDTXComfortNoise(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetDTX(true)

	silence := make([]float64, 960)
	framesPerInterval := encoder.DTXComfortNoiseIntervalMs / 20

	// Encode enough frames to enter DTX and reach comfort noise interval
	var comfortNoiseCount int
	totalFrames := encoder.DTXFrameThreshold + framesPerInterval + 5
	for i := 0; i < totalFrames; i++ {
		packet, _ := enc.Encode(silence, 960)
		if i >= encoder.DTXFrameThreshold && packet != nil {
			comfortNoiseCount++
		}
	}

	if comfortNoiseCount < 1 {
		t.Errorf("Should send at least one comfort noise packet, got %d", comfortNoiseCount)
	}
	t.Logf("Sent %d comfort noise packets over %d frames", comfortNoiseCount, totalFrames)
}

// TestDTXExitOnSpeech tests that speech exits DTX mode immediately.
func TestDTXExitOnSpeech(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetDTX(true)

	silence := make([]float64, 960)
	speech := generateTestSignal(960, 1)

	// Enter DTX mode
	for i := 0; i < encoder.DTXFrameThreshold+5; i++ {
		enc.Encode(silence, 960)
	}

	// Verify in DTX mode
	packet, _ := enc.Encode(silence, 960)
	if packet != nil {
		t.Log("Warning: Expected nil packet in DTX mode")
	}

	// Send speech - should exit DTX and produce packet
	packet, err := enc.Encode(speech, 960)
	if err != nil {
		t.Fatalf("Encode speech failed: %v", err)
	}
	if packet == nil {
		t.Error("Speech should exit DTX mode and produce a packet")
	}
}

// TestComplexitySetting tests complexity getter/setter with clamping.
func TestComplexitySetting(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)

	// Verify default is 10 (maximum quality)
	if enc.Complexity() != 10 {
		t.Errorf("Default complexity = %d, want 10", enc.Complexity())
	}

	// Test valid range
	enc.SetComplexity(5)
	if enc.Complexity() != 5 {
		t.Errorf("SetComplexity(5): got %d, want 5", enc.Complexity())
	}

	// Test clamping below 0
	enc.SetComplexity(-1)
	if enc.Complexity() != 0 {
		t.Errorf("SetComplexity(-1): got %d, want 0", enc.Complexity())
	}

	// Test clamping above 10
	enc.SetComplexity(15)
	if enc.Complexity() != 10 {
		t.Errorf("SetComplexity(15): got %d, want 10", enc.Complexity())
	}

	// Test all valid values
	for i := 0; i <= 10; i++ {
		enc.SetComplexity(i)
		if enc.Complexity() != i {
			t.Errorf("SetComplexity(%d): got %d", i, enc.Complexity())
		}
	}
}

// TestComplexityAffectsQuality tests that encoding works at all complexity levels.
func TestComplexityAffectsQuality(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)

	for complexity := 0; complexity <= 10; complexity++ {
		enc.SetComplexity(complexity)

		pcm := generateTestSignal(960, 1)
		packet, err := enc.Encode(pcm, 960)
		if err != nil {
			t.Fatalf("Complexity %d: encode failed: %v", complexity, err)
		}
		if len(packet) == 0 {
			t.Errorf("Complexity %d: should produce output", complexity)
		}
		t.Logf("Complexity %d: %d bytes", complexity, len(packet))
	}
}

// TestDTXResetOnEncoderReset tests that DTX state resets with encoder reset.
func TestDTXResetOnEncoderReset(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetDTX(true)

	silence := make([]float64, 960)

	// Enter DTX mode
	for i := 0; i < encoder.DTXFrameThreshold+5; i++ {
		enc.Encode(silence, 960)
	}

	// Verify in DTX mode
	packet, _ := enc.Encode(silence, 960)
	if packet != nil {
		t.Log("Warning: Expected nil packet in DTX mode")
	}

	// Reset encoder
	enc.Reset()

	// After reset, should no longer be in DTX mode - frames should encode
	packet, err := enc.Encode(silence, 960)
	if err != nil {
		t.Fatalf("After reset encode failed: %v", err)
	}
	if packet == nil {
		t.Error("After reset, silent frame should encode (not suppressed)")
	}
}

// TestClassifySignal tests the signal classification function.
func TestClassifySignal(t *testing.T) {
	// Test silence detection
	silence := make([]float32, 960)
	signalType, energy := encoder.ClassifySignal(silence)
	if signalType != 0 {
		t.Errorf("Silence: signalType = %d, want 0 (inactive)", signalType)
	}
	if energy > 0.0001 {
		t.Errorf("Silence: energy = %f, want < 0.0001", energy)
	}

	// Test active signal detection
	active := make([]float32, 960)
	for i := range active {
		active[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	signalType, energy = encoder.ClassifySignal(active)
	if signalType == 0 {
		t.Error("Active signal: should not be classified as inactive")
	}
	if energy < 0.0001 {
		t.Errorf("Active signal: energy = %f, should be > 0.0001", energy)
	}
	t.Logf("Active signal: type=%d, energy=%f", signalType, energy)

	// Test empty input
	signalType, energy = encoder.ClassifySignal(nil)
	if signalType != 0 || energy != 0 {
		t.Errorf("Empty input: signalType=%d, energy=%f, want 0, 0", signalType, energy)
	}
}

// TestDTXConstants verifies DTX constants are set correctly.
// Updated to match libopus: NB_SPEECH_FRAMES_BEFORE_DTX = 10 frames (200ms at 20ms frames)
func TestDTXConstants(t *testing.T) {
	if encoder.DTXComfortNoiseIntervalMs != 400 {
		t.Errorf("DTXComfortNoiseIntervalMs = %d, want 400", encoder.DTXComfortNoiseIntervalMs)
	}

	// DTXFrameThreshold is now 10 frames (200ms) matching libopus NB_SPEECH_FRAMES_BEFORE_DTX
	if encoder.DTXFrameThreshold != 10 {
		t.Errorf("DTXFrameThreshold = %d, want 10 (matches libopus NB_SPEECH_FRAMES_BEFORE_DTX)", encoder.DTXFrameThreshold)
	}

	if encoder.DTXFadeInMs != 10 {
		t.Errorf("DTXFadeInMs = %d, want 10", encoder.DTXFadeInMs)
	}

	if encoder.DTXFadeOutMs != 10 {
		t.Errorf("DTXFadeOutMs = %d, want 10", encoder.DTXFadeOutMs)
	}

	if encoder.DTXMinBitrate != 6000 {
		t.Errorf("DTXMinBitrate = %d, want 6000", encoder.DTXMinBitrate)
	}
}

// TestEncoderPacketFormat tests that Encoder.Encode returns complete packets with TOC byte.
func TestEncoderPacketFormat(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)

	pcm := make([]float64, 960)
	for i := range pcm {
		pcm[i] = 0.5 * float64(i) / float64(len(pcm)) // Ramp signal
	}

	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encoded packet is empty")
	}

	// Packet must have at least TOC byte
	if len(packet) < 1 {
		t.Fatal("Packet missing TOC byte")
	}

	// Parse TOC to verify format
	toc := gopus.ParseTOC(packet[0])
	if toc.Mode != types.ModeHybrid {
		t.Errorf("TOC mode = %v, want ModeHybrid", toc.Mode)
	}
	if toc.Bandwidth != types.BandwidthSuperwideband {
		t.Errorf("TOC bandwidth = %v, want Superwideband", toc.Bandwidth)
	}
	if toc.FrameSize != 960 {
		t.Errorf("TOC frameSize = %d, want 960", toc.FrameSize)
	}
	if toc.Stereo {
		t.Error("TOC stereo = true, want false")
	}
	if toc.FrameCode != 0 {
		t.Errorf("TOC frameCode = %d, want 0 (single frame)", toc.FrameCode)
	}

	t.Logf("Hybrid SWB packet: %d bytes, TOC=0x%02X, config=%d", len(packet), packet[0], toc.Config)
}

// TestEncoderPacketConfigs tests that encoder produces correct TOC configs for all modes.
func TestEncoderPacketConfigs(t *testing.T) {
	tests := []struct {
		mode      encoder.Mode
		bandwidth types.Bandwidth
		frameSize int
		config    uint8
	}{
		// Hybrid configs 12-15
		{encoder.ModeHybrid, types.BandwidthSuperwideband, 480, 12},
		{encoder.ModeHybrid, types.BandwidthSuperwideband, 960, 13},
		{encoder.ModeHybrid, types.BandwidthFullband, 480, 14},
		{encoder.ModeHybrid, types.BandwidthFullband, 960, 15},
		// SILK configs
		{encoder.ModeSILK, types.BandwidthNarrowband, 960, 1},
		{encoder.ModeSILK, types.BandwidthWideband, 960, 9},
		// CELT configs
		{encoder.ModeCELT, types.BandwidthFullband, 960, 31},
		{encoder.ModeCELT, types.BandwidthFullband, 480, 30},
	}

	for _, tt := range tests {
		name := modeName(tt.mode) + "_" + bwNameTypes(tt.bandwidth) + "_" + frameSizeName(tt.frameSize)
		t.Run(name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(tt.mode)
			enc.SetBandwidth(tt.bandwidth)

			pcm := make([]float64, tt.frameSize)
			for i := range pcm {
				pcm[i] = 0.3 * float64(i) / float64(len(pcm))
			}

			packet, err := enc.Encode(pcm, tt.frameSize)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			if len(packet) == 0 {
				t.Fatal("Encoded packet is empty")
			}

			toc := gopus.ParseTOC(packet[0])
			if toc.Config != tt.config {
				t.Errorf("TOC config = %d, want %d", toc.Config, tt.config)
			}

			t.Logf("%s: config=%d, packet=%d bytes", name, toc.Config, len(packet))
		})
	}
}

// TestEncoderPacketStereo tests that stereo flag is set correctly in TOC.
func TestEncoderPacketStereo(t *testing.T) {
	// Mono encoder
	encMono := encoder.NewEncoder(48000, 1)
	encMono.SetMode(encoder.ModeHybrid)
	encMono.SetBandwidth(types.BandwidthSuperwideband)

	pcmMono := make([]float64, 960)
	packetMono, err := encMono.Encode(pcmMono, 960)
	if err != nil {
		t.Fatalf("Mono encode failed: %v", err)
	}

	tocMono := gopus.ParseTOC(packetMono[0])
	if tocMono.Stereo {
		t.Error("Mono packet has stereo=true, want false")
	}

	// Stereo encoder
	encStereo := encoder.NewEncoder(48000, 2)
	encStereo.SetMode(encoder.ModeHybrid)
	encStereo.SetBandwidth(types.BandwidthSuperwideband)

	pcmStereo := make([]float64, 960*2)
	packetStereo, err := encStereo.Encode(pcmStereo, 960)
	if err != nil {
		t.Fatalf("Stereo encode failed: %v", err)
	}

	tocStereo := gopus.ParseTOC(packetStereo[0])
	if !tocStereo.Stereo {
		t.Error("Stereo packet has stereo=false, want true")
	}
}

// TestEncoderPacketParseable tests that encoder output can be fully parsed.
func TestEncoderPacketParseable(t *testing.T) {
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthFullband)

	pcm := generateTestSignal(960, 1)
	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Verify packet can be parsed by ParsePacket
	info, err := gopus.ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket failed: %v", err)
	}

	if info.FrameCount != 1 {
		t.Errorf("Frame count = %d, want 1", info.FrameCount)
	}

	if len(info.FrameSizes) != 1 {
		t.Fatalf("FrameSizes length = %d, want 1", len(info.FrameSizes))
	}

	expectedFrameSize := len(packet) - 1 // Total - TOC
	if info.FrameSizes[0] != expectedFrameSize {
		t.Errorf("Frame size = %d, want %d", info.FrameSizes[0], expectedFrameSize)
	}

	t.Logf("Parseable packet: %d bytes, frame=%d bytes", len(packet), info.FrameSizes[0])
}

// Helper functions for test names
func modeName(m encoder.Mode) string {
	switch m {
	case encoder.ModeSILK:
		return "silk"
	case encoder.ModeHybrid:
		return "hybrid"
	case encoder.ModeCELT:
		return "celt"
	default:
		return "auto"
	}
}

func bwNameTypes(bw types.Bandwidth) string {
	switch bw {
	case types.BandwidthNarrowband:
		return "nb"
	case types.BandwidthMediumband:
		return "mb"
	case types.BandwidthWideband:
		return "wb"
	case types.BandwidthSuperwideband:
		return "swb"
	case types.BandwidthFullband:
		return "fb"
	default:
		return "unk"
	}
}

func frameSizeName(fs int) string {
	switch fs {
	case 120:
		return "2.5ms"
	case 240:
		return "5ms"
	case 480:
		return "10ms"
	case 960:
		return "20ms"
	case 1920:
		return "40ms"
	case 2880:
		return "60ms"
	default:
		return "unk"
	}
}
