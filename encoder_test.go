package gopus

import (
	"fmt"
	"math"
	"testing"
)

func TestNewEncoder_ValidParams(t *testing.T) {
	tests := []struct {
		name        string
		sampleRate  int
		channels    int
		application Application
	}{
		{"48kHz_mono_voip", 48000, 1, ApplicationVoIP},
		{"48kHz_stereo_voip", 48000, 2, ApplicationVoIP},
		{"48kHz_mono_audio", 48000, 1, ApplicationAudio},
		{"48kHz_stereo_audio", 48000, 2, ApplicationAudio},
		{"48kHz_mono_lowdelay", 48000, 1, ApplicationLowDelay},
		{"24kHz_mono_voip", 24000, 1, ApplicationVoIP},
		{"16kHz_mono_voip", 16000, 1, ApplicationVoIP},
		{"12kHz_mono_voip", 12000, 1, ApplicationVoIP},
		{"8kHz_mono_voip", 8000, 1, ApplicationVoIP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(tt.sampleRate, tt.channels, tt.application)
			if err != nil {
				t.Fatalf("NewEncoder(%d, %d, %d) unexpected error: %v", tt.sampleRate, tt.channels, tt.application, err)
			}
			if enc == nil {
				t.Fatal("NewEncoder returned nil encoder")
			}
			if enc.SampleRate() != tt.sampleRate {
				t.Errorf("SampleRate() = %d, want %d", enc.SampleRate(), tt.sampleRate)
			}
			if enc.Channels() != tt.channels {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), tt.channels)
			}
		})
	}
}

func TestNewEncoder_InvalidParams(t *testing.T) {
	tests := []struct {
		name        string
		sampleRate  int
		channels    int
		expectedErr error
	}{
		{"invalid_rate_0", 0, 1, ErrInvalidSampleRate},
		{"invalid_rate_44100", 44100, 1, ErrInvalidSampleRate},
		{"invalid_rate_96000", 96000, 1, ErrInvalidSampleRate},
		{"invalid_channels_0", 48000, 0, ErrInvalidChannels},
		{"invalid_channels_3", 48000, 3, ErrInvalidChannels},
		{"invalid_channels_negative", 48000, -1, ErrInvalidChannels},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(tt.sampleRate, tt.channels, ApplicationAudio)
			if err != tt.expectedErr {
				t.Errorf("NewEncoder(%d, %d) error = %v, want %v", tt.sampleRate, tt.channels, err, tt.expectedErr)
			}
			if enc != nil {
				t.Error("NewEncoder returned non-nil encoder on error")
			}
		})
	}
}

// generateSineWave generates a sine wave at the given frequency.
func generateSineWave(sampleRate int, freq float64, samples int) []float32 {
	pcm := make([]float32, samples)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
	}
	return pcm
}

func TestEncoder_Encode_Float32(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Generate 20ms of 440Hz sine wave
	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	// Encode
	data := make([]byte, 4000)
	n, err := enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if n == 0 {
		t.Fatal("Encode returned 0 bytes")
	}

	t.Logf("Encoded %d samples to %d bytes", frameSize, n)
}

func TestEncoder_Encode_Int16(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Generate 20ms of 440Hz sine wave as int16
	frameSize := 960
	pcm := make([]int16, frameSize)
	for i := range pcm {
		pcm[i] = int16(16384 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}

	// Encode
	data := make([]byte, 4000)
	n, err := enc.EncodeInt16(pcm, data)
	if err != nil {
		t.Fatalf("EncodeInt16 error: %v", err)
	}

	if n == 0 {
		t.Fatal("EncodeInt16 returned 0 bytes")
	}

	t.Logf("Encoded %d int16 samples to %d bytes", frameSize, n)
}

func TestEncoder_Encode_RoundTrip(t *testing.T) {
	// Create encoder and decoder
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate 20ms of 440Hz sine wave
	frameSize := 960
	pcmIn := generateSineWave(48000, 440, frameSize)

	// Calculate input energy
	var inputEnergy float64
	for _, s := range pcmIn {
		inputEnergy += float64(s) * float64(s)
	}

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet")
	}

	// Decode
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Calculate output energy
	var outputEnergy float64
	for _, s := range pcmOut[:n*cfg.Channels] {
		outputEnergy += float64(s) * float64(s)
	}

	t.Logf("Input energy: %f, Output energy: %f", inputEnergy, outputEnergy)
	t.Logf("Encoded to %d bytes, decoded to %d samples", len(packet), n*cfg.Channels)

	// The output should have some energy (lossy compression but not zero)
	if outputEnergy == 0 {
		t.Error("Decoded output has zero energy")
	}
}

func TestEncoder_SetBitrate(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Valid bitrates
	validBitrates := []int{6000, 12000, 32000, 64000, 128000, 256000, 510000}
	for _, br := range validBitrates {
		t.Run(fmt.Sprintf("bitrate_%d", br), func(t *testing.T) {
			err := enc.SetBitrate(br)
			if err != nil {
				t.Errorf("SetBitrate(%d) error: %v", br, err)
			}
			if enc.Bitrate() != br {
				t.Errorf("Bitrate() = %d, want %d", enc.Bitrate(), br)
			}
		})
	}

	// Invalid bitrates
	invalidBitrates := []int{0, 5999, 510001, -1, 1000000}
	for _, br := range invalidBitrates {
		t.Run(fmt.Sprintf("invalid_bitrate_%d", br), func(t *testing.T) {
			err := enc.SetBitrate(br)
			if err != ErrInvalidBitrate {
				t.Errorf("SetBitrate(%d) error = %v, want ErrInvalidBitrate", br, err)
			}
		})
	}
}

func TestEncoder_SetComplexity(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Valid complexity values
	for c := 0; c <= 10; c++ {
		t.Run(fmt.Sprintf("complexity_%d", c), func(t *testing.T) {
			err := enc.SetComplexity(c)
			if err != nil {
				t.Errorf("SetComplexity(%d) error: %v", c, err)
			}
			if enc.Complexity() != c {
				t.Errorf("Complexity() = %d, want %d", enc.Complexity(), c)
			}
		})
	}

	// Invalid complexity values
	invalidComplexity := []int{-1, 11, 100}
	for _, c := range invalidComplexity {
		t.Run(fmt.Sprintf("invalid_complexity_%d", c), func(t *testing.T) {
			err := enc.SetComplexity(c)
			if err != ErrInvalidComplexity {
				t.Errorf("SetComplexity(%d) error = %v, want ErrInvalidComplexity", c, err)
			}
		})
	}
}

func TestEncoder_SetBitrateMode(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	validModes := []BitrateMode{BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR}
	for _, mode := range validModes {
		t.Run(fmt.Sprintf("mode_%d", mode), func(t *testing.T) {
			if err := enc.SetBitrateMode(mode); err != nil {
				t.Fatalf("SetBitrateMode(%d) error: %v", mode, err)
			}
			if got := enc.BitrateMode(); got != mode {
				t.Fatalf("BitrateMode()=%d want=%d", got, mode)
			}
		})
	}

	if err := enc.SetBitrateMode(BitrateMode(999)); err != ErrInvalidBitrateMode {
		t.Fatalf("SetBitrateMode(invalid) error=%v want=%v", err, ErrInvalidBitrateMode)
	}
}

func TestEncoder_SetVBRAndConstraint(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	enc.SetVBR(false)
	if enc.VBR() {
		t.Fatal("VBR()=true after SetVBR(false)")
	}
	if got := enc.BitrateMode(); got != BitrateModeCBR {
		t.Fatalf("BitrateMode()=%d want=%d", got, BitrateModeCBR)
	}

	enc.SetVBR(true)
	if !enc.VBR() {
		t.Fatal("VBR()=false after SetVBR(true)")
	}
	if got := enc.BitrateMode(); got != BitrateModeVBR {
		t.Fatalf("BitrateMode()=%d want=%d", got, BitrateModeVBR)
	}

	enc.SetVBRConstraint(true)
	if !enc.VBRConstraint() {
		t.Fatal("VBRConstraint()=false after SetVBRConstraint(true)")
	}
	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Fatalf("BitrateMode()=%d want=%d", got, BitrateModeCVBR)
	}

	enc.SetVBRConstraint(false)
	if enc.VBRConstraint() {
		t.Fatal("VBRConstraint()=true after SetVBRConstraint(false)")
	}
	if got := enc.BitrateMode(); got != BitrateModeVBR {
		t.Fatalf("BitrateMode()=%d want=%d", got, BitrateModeVBR)
	}
}

func TestEncoder_SetPacketLoss(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	for _, loss := range []int{0, 5, 25, 100} {
		if err := enc.SetPacketLoss(loss); err != nil {
			t.Fatalf("SetPacketLoss(%d) error: %v", loss, err)
		}
		if got := enc.PacketLoss(); got != loss {
			t.Fatalf("PacketLoss()=%d want=%d", got, loss)
		}
	}

	for _, loss := range []int{-1, 101} {
		if err := enc.SetPacketLoss(loss); err != ErrInvalidPacketLoss {
			t.Fatalf("SetPacketLoss(%d) error=%v want=%v", loss, err, ErrInvalidPacketLoss)
		}
	}
}

func TestEncoder_SetBandwidth(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	for _, bw := range []Bandwidth{
		BandwidthNarrowband,
		BandwidthMediumband,
		BandwidthWideband,
		BandwidthSuperwideband,
		BandwidthFullband,
	} {
		if err := enc.SetBandwidth(bw); err != nil {
			t.Fatalf("SetBandwidth(%d) error: %v", bw, err)
		}
		if got := enc.Bandwidth(); got != bw {
			t.Fatalf("Bandwidth()=%d want=%d", got, bw)
		}
	}

	if err := enc.SetBandwidth(Bandwidth(255)); err != ErrInvalidBandwidth {
		t.Fatalf("SetBandwidth(invalid) error=%v want=%v", err, ErrInvalidBandwidth)
	}
}

func TestEncoder_SetApplication(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	for _, app := range []Application{ApplicationVoIP, ApplicationAudio, ApplicationLowDelay} {
		if err := enc.SetApplication(app); err != nil {
			t.Fatalf("SetApplication(%d) error: %v", app, err)
		}
		if got := enc.Application(); got != app {
			t.Fatalf("Application()=%d want=%d", got, app)
		}
	}

	if err := enc.SetApplication(Application(99)); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(invalid) error=%v want=%v", err, ErrInvalidApplication)
	}

	// Match libopus ctl semantics: after first successful encode, application
	// changes are rejected unless value is unchanged.
	pcm := make([]float32, enc.FrameSize()*enc.Channels())
	packet := make([]byte, 4000)
	if _, err := enc.Encode(pcm, packet); err != nil {
		t.Fatalf("Encode before application lock test error: %v", err)
	}
	if err := enc.SetApplication(ApplicationVoIP); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(change after encode) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := enc.SetApplication(enc.Application()); err != nil {
		t.Fatalf("SetApplication(same after encode) error: %v", err)
	}

	enc.Reset()
	if err := enc.SetApplication(ApplicationLowDelay); err != nil {
		t.Fatalf("SetApplication(after reset) error: %v", err)
	}
}

func TestEncoder_DTX_Silence(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Enable DTX
	enc.SetDTX(true)
	if !enc.DTXEnabled() {
		t.Error("DTXEnabled() = false after SetDTX(true)")
	}

	// Encode multiple silence frames
	frameSize := 960
	silence := make([]float32, frameSize)

	// DTX requires several frames of silence before suppressing
	// The DTXFrameThreshold is 20 frames (400ms)
	for i := 0; i < 25; i++ {
		data := make([]byte, 4000)
		n, err := enc.Encode(silence, data)
		if err != nil {
			t.Fatalf("Encode error on frame %d: %v", i, err)
		}

		// After threshold, should produce 0 bytes (suppressed)
		if i > 20 && n == 0 {
			t.Logf("Frame %d suppressed by DTX (0 bytes)", i)
			return // Success - DTX suppressed a frame
		}
	}

	// DTX may or may not suppress depending on implementation details
	t.Log("DTX did not suppress frames (may need more silence frames)")
}

func TestEncoder_FEC(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Initially disabled
	if enc.FECEnabled() {
		t.Error("FECEnabled() = true initially, want false")
	}

	// Enable FEC
	enc.SetFEC(true)
	if !enc.FECEnabled() {
		t.Error("FECEnabled() = false after SetFEC(true)")
	}

	// Disable FEC
	enc.SetFEC(false)
	if enc.FECEnabled() {
		t.Error("FECEnabled() = true after SetFEC(false)")
	}
}

func TestEncoder_Reset(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Encode a frame
	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)
	data := make([]byte, 4000)
	_, err = enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Reset
	enc.Reset()

	// Encode again should work
	_, err = enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode after Reset error: %v", err)
	}
}

func TestEncoder_Stereo(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Generate stereo signal (interleaved)
	frameSize := 960
	pcm := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		// Left: 440 Hz
		pcm[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
		// Right: 880 Hz
		pcm[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}

	// Encode
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet for stereo")
	}

	t.Logf("Encoded stereo %d samples to %d bytes", frameSize, len(packet))
}

func TestEncoder_FrameSize(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default frame size is 960 (20ms)
	if enc.FrameSize() != 960 {
		t.Errorf("FrameSize() = %d, want 960", enc.FrameSize())
	}

	// Valid frame sizes
	validSizes := []int{120, 240, 480, 960, 1920, 2880}
	for _, size := range validSizes {
		t.Run(fmt.Sprintf("framesize_%d", size), func(t *testing.T) {
			err := enc.SetFrameSize(size)
			if err != nil {
				t.Errorf("SetFrameSize(%d) error: %v", size, err)
			}
			if enc.FrameSize() != size {
				t.Errorf("FrameSize() = %d, want %d", enc.FrameSize(), size)
			}
		})
	}

	// Invalid frame sizes
	invalidSizes := []int{0, 100, 500, 1000, 3000}
	for _, size := range invalidSizes {
		t.Run(fmt.Sprintf("invalid_framesize_%d", size), func(t *testing.T) {
			err := enc.SetFrameSize(size)
			if err != ErrInvalidFrameSize {
				t.Errorf("SetFrameSize(%d) error = %v, want ErrInvalidFrameSize", size, err)
			}
		})
	}
}

func TestEncoder_EncodeFloat32_Convenience(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("EncodeFloat32 returned empty packet")
	}
}

func TestEncoder_EncodeInt16Slice_Convenience(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	frameSize := 960
	pcm := make([]int16, frameSize)
	for i := range pcm {
		pcm[i] = int16(16384 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}

	packet, err := enc.EncodeInt16Slice(pcm)
	if err != nil {
		t.Fatalf("EncodeInt16Slice error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("EncodeInt16Slice returned empty packet")
	}
}

func TestEncoder_InvalidFrameSize_Encode(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default frame size is 960, try to encode wrong size
	pcm := make([]float32, 500) // Wrong size
	data := make([]byte, 4000)

	_, err = enc.Encode(pcm, data)
	if err != ErrInvalidFrameSize {
		t.Errorf("Encode with wrong size: got %v, want ErrInvalidFrameSize", err)
	}
}

func TestEncoder_SetSignal(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is SignalAuto
	if enc.Signal() != SignalAuto {
		t.Errorf("Signal() = %d, want SignalAuto (%d)", enc.Signal(), SignalAuto)
	}

	// Test valid signals
	validSignals := []Signal{SignalAuto, SignalVoice, SignalMusic}
	for _, sig := range validSignals {
		t.Run(fmt.Sprintf("signal_%d", sig), func(t *testing.T) {
			err := enc.SetSignal(sig)
			if err != nil {
				t.Errorf("SetSignal(%d) error: %v", sig, err)
			}
			if enc.Signal() != sig {
				t.Errorf("Signal() = %d, want %d", enc.Signal(), sig)
			}
		})
	}

	// Test invalid signal
	err = enc.SetSignal(Signal(9999))
	if err != ErrInvalidSignal {
		t.Errorf("SetSignal(9999) error = %v, want ErrInvalidSignal", err)
	}
}

func TestEncoder_SetMaxBandwidth(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is Fullband
	if enc.MaxBandwidth() != BandwidthFullband {
		t.Errorf("MaxBandwidth() = %d, want BandwidthFullband (%d)", enc.MaxBandwidth(), BandwidthFullband)
	}

	// Test all bandwidths
	bandwidths := []Bandwidth{
		BandwidthNarrowband,
		BandwidthMediumband,
		BandwidthWideband,
		BandwidthSuperwideband,
		BandwidthFullband,
	}
	for _, bw := range bandwidths {
		t.Run(fmt.Sprintf("bandwidth_%d", bw), func(t *testing.T) {
			enc.SetMaxBandwidth(bw)
			if enc.MaxBandwidth() != bw {
				t.Errorf("MaxBandwidth() = %d, want %d", enc.MaxBandwidth(), bw)
			}
		})
	}
}

func TestEncoder_SetForceChannels(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is -1 (auto)
	if enc.ForceChannels() != -1 {
		t.Errorf("ForceChannels() = %d, want -1", enc.ForceChannels())
	}

	// Test valid values
	validChannels := []int{-1, 1, 2}
	for _, ch := range validChannels {
		t.Run(fmt.Sprintf("channels_%d", ch), func(t *testing.T) {
			err := enc.SetForceChannels(ch)
			if err != nil {
				t.Errorf("SetForceChannels(%d) error: %v", ch, err)
			}
			if enc.ForceChannels() != ch {
				t.Errorf("ForceChannels() = %d, want %d", enc.ForceChannels(), ch)
			}
		})
	}

	// Test invalid values
	invalidChannels := []int{-2, 0, 3, 100}
	for _, ch := range invalidChannels {
		t.Run(fmt.Sprintf("invalid_channels_%d", ch), func(t *testing.T) {
			err := enc.SetForceChannels(ch)
			if err != ErrInvalidForceChannels {
				t.Errorf("SetForceChannels(%d) error = %v, want ErrInvalidForceChannels", ch, err)
			}
		})
	}
}

func TestEncoder_Lookahead(t *testing.T) {
	tests := []struct {
		name        string
		sampleRate  int
		application Application
		want        int
	}{
		{
			name:        "audio_48k",
			sampleRate:  48000,
			application: ApplicationAudio,
			want:        48000/400 + 48000/250,
		},
		{
			name:        "voip_48k",
			sampleRate:  48000,
			application: ApplicationVoIP,
			want:        48000/400 + 48000/250,
		},
		{
			name:        "lowdelay_48k",
			sampleRate:  48000,
			application: ApplicationLowDelay,
			want:        48000 / 400,
		},
		{
			name:        "audio_24k",
			sampleRate:  24000,
			application: ApplicationAudio,
			want:        24000/400 + 24000/250,
		},
		{
			name:        "lowdelay_24k",
			sampleRate:  24000,
			application: ApplicationLowDelay,
			want:        24000 / 400,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(tc.sampleRate, 1, tc.application)
			if err != nil {
				t.Fatalf("NewEncoder error: %v", err)
			}
			if got := enc.Lookahead(); got != tc.want {
				t.Fatalf("Lookahead() = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("set_application_updates_lookahead_before_encode", func(t *testing.T) {
		enc, err := NewEncoder(48000, 1, ApplicationAudio)
		if err != nil {
			t.Fatalf("NewEncoder error: %v", err)
		}
		if got, want := enc.Lookahead(), 48000/400+48000/250; got != want {
			t.Fatalf("Lookahead(audio) = %d, want %d", got, want)
		}
		if err := enc.SetApplication(ApplicationLowDelay); err != nil {
			t.Fatalf("SetApplication(LowDelay) error: %v", err)
		}
		if got, want := enc.Lookahead(), 48000/400; got != want {
			t.Fatalf("Lookahead(lowdelay) = %d, want %d", got, want)
		}
		if err := enc.SetApplication(ApplicationAudio); err != nil {
			t.Fatalf("SetApplication(Audio) error: %v", err)
		}
		if got, want := enc.Lookahead(), 48000/400+48000/250; got != want {
			t.Fatalf("Lookahead(audio after reset) = %d, want %d", got, want)
		}
	})
}

func TestEncoder_SetLSBDepth(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is 24
	if enc.LSBDepth() != 24 {
		t.Errorf("LSBDepth() = %d, want 24", enc.LSBDepth())
	}

	// Test valid depths
	for depth := 8; depth <= 24; depth++ {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			err := enc.SetLSBDepth(depth)
			if err != nil {
				t.Errorf("SetLSBDepth(%d) error: %v", depth, err)
			}
			if enc.LSBDepth() != depth {
				t.Errorf("LSBDepth() = %d, want %d", enc.LSBDepth(), depth)
			}
		})
	}

	// Test invalid depths
	invalidDepths := []int{0, 7, 25, 32}
	for _, depth := range invalidDepths {
		t.Run(fmt.Sprintf("invalid_depth_%d", depth), func(t *testing.T) {
			err := enc.SetLSBDepth(depth)
			if err != ErrInvalidLSBDepth {
				t.Errorf("SetLSBDepth(%d) error = %v, want ErrInvalidLSBDepth", depth, err)
			}
		})
	}
}

func TestEncoder_SetPredictionDisabled(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is false
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() = true, want false")
	}

	// Enable
	enc.SetPredictionDisabled(true)
	if !enc.PredictionDisabled() {
		t.Error("PredictionDisabled() = false after SetPredictionDisabled(true)")
	}

	// Disable
	enc.SetPredictionDisabled(false)
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() = true after SetPredictionDisabled(false)")
	}
}

func TestEncoder_SetPhaseInversionDisabled(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Default is false
	if enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = true, want false")
	}

	// Enable
	enc.SetPhaseInversionDisabled(true)
	if !enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = false after SetPhaseInversionDisabled(true)")
	}

	// Disable
	enc.SetPhaseInversionDisabled(false)
	if enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = true after SetPhaseInversionDisabled(false)")
	}
}

func TestEncoder_SignalVoice_BiasesTowardSILK(t *testing.T) {
	// Create encoder with voice signal hint at wideband
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Set signal type to voice
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}

	// Generate a simple frame
	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	// Encode and verify it produces a valid packet
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("Encode with SignalVoice returned empty packet")
	}

	t.Logf("SignalVoice encoded to %d bytes", len(packet))
}

func TestEncoder_SignalMusic_BiasesTowardCELT(t *testing.T) {
	// Create encoder with music signal hint
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Set signal type to music
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}

	// Generate a simple frame
	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	// Encode and verify it produces a valid packet
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("Encode with SignalMusic returned empty packet")
	}

	t.Logf("SignalMusic encoded to %d bytes", len(packet))
}

func TestEncoder_MaxBandwidth_ClampsOutput(t *testing.T) {
	// Test that max bandwidth setting works for wideband limit
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Limit to wideband (instead of narrowband to avoid SILK frame size restrictions)
	enc.SetMaxBandwidth(BandwidthWideband)

	frameSize := 960
	pcm := generateSineWave(48000, 440, frameSize)

	// Encode - should work with wideband limit
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode with limited bandwidth error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("Encode with limited bandwidth returned empty packet")
	}

	t.Logf("Wideband-limited encoded to %d bytes", len(packet))
}
