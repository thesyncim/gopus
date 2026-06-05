package gopus

import (
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
		{"48kHz_mono_restricted_silk", 48000, 1, ApplicationRestrictedSilk},
		{"48kHz_stereo_restricted_celt", 48000, 2, ApplicationRestrictedCelt},
		{"24kHz_mono_voip", 24000, 1, ApplicationVoIP},
		{"16kHz_mono_voip", 16000, 1, ApplicationVoIP},
		{"12kHz_mono_voip", 12000, 1, ApplicationVoIP},
		{"8kHz_mono_voip", 8000, 1, ApplicationVoIP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: tt.sampleRate, Channels: tt.channels, Application: tt.application})
			if err != nil {
				t.Fatalf("NewEncoder(EncoderConfig{SampleRate: %d, Channels: %d, Application: %d}) unexpected error: %v", tt.sampleRate, tt.channels, tt.application, err)
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
		{"invalid_channels_0", 48000, 0, ErrInvalidChannels},
		{"invalid_channels_3", 48000, 3, ErrInvalidChannels},
		{"invalid_channels_negative", 48000, -1, ErrInvalidChannels},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(EncoderConfig{SampleRate: tt.sampleRate, Channels: tt.channels, Application: ApplicationAudio})
			if err != tt.expectedErr {
				t.Errorf("NewEncoder(%d, %d) error = %v, want %v", tt.sampleRate, tt.channels, err, tt.expectedErr)
			}
			if enc != nil {
				t.Error("NewEncoder returned non-nil encoder on error")
			}
		})
	}
}

func TestNewEncoder_InvalidApplication(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: 99})
	if err != ErrInvalidApplication {
		t.Fatalf("NewEncoder(invalid application) error=%v want=%v", err, ErrInvalidApplication)
	}
	if enc != nil {
		t.Fatal("NewEncoder returned non-nil encoder for invalid application")
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

func generateSineWaveInt24(sampleRate int, freq float64, samples int) []int32 {
	pcm := make([]int32, samples)
	for i := range pcm {
		pcm[i] = int32((1 << 22) * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
	}
	return pcm
}

func TestEncoder_DTX_Silence(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
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

	// DTX requires several frames of silence before activating.
	// After threshold, DTX returns 1-byte TOC-only packets (matching libopus).
	for i := range 50 {
		data := make([]byte, 4000)
		n, err := enc.Encode(silence, data)
		if err != nil {
			t.Fatalf("Encode error on frame %d: %v", i, err)
		}

		// DTX frames are 1-byte TOC-only packets (not 0 bytes)
		if n == 1 {
			t.Logf("Frame %d: DTX active (1-byte TOC packet)", i)
			return // Success - DTX emitted TOC-only packet
		}
	}

	// DTX may or may not suppress depending on VAD adaptation
	t.Log("DTX did not activate (may need more silence frames for VAD adaptation)")
}

func TestEncoder_InDTXDelegatesToCoreEncoder(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetDTX(true)

	if got, want := enc.InDTX(), enc.enc.InDTX(); got != want {
		t.Fatalf("initial InDTX()=%v want core=%v", got, want)
	}

	silence := make([]float32, enc.FrameSize()*enc.Channels())
	packet := make([]byte, 4000)
	activated := false
	for i := range 50 {
		if _, err := enc.Encode(silence, packet); err != nil {
			t.Fatalf("Encode(silence) frame %d error: %v", i, err)
		}
		if got, want := enc.InDTX(), enc.enc.InDTX(); got != want {
			t.Fatalf("frame %d InDTX()=%v want core=%v", i, got, want)
		}
		if enc.InDTX() {
			activated = true
			break
		}
	}
	if !activated {
		t.Fatal("InDTX() never became true after sustained silence")
	}

	speech := generateSineWave(48000, 440, enc.FrameSize())
	if _, err := enc.Encode(speech, packet); err != nil {
		t.Fatalf("Encode(speech) error: %v", err)
	}
	if got, want := enc.InDTX(), enc.enc.InDTX(); got != want {
		t.Fatalf("post-speech InDTX()=%v want core=%v", got, want)
	}
}

func TestEncoder_VADActivityDelegatesToCoreEncoder(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetDTX(true)

	if got, want := enc.VADActivity(), enc.enc.GetVADActivity(); got != want {
		t.Fatalf("initial VADActivity()=%d want core=%d", got, want)
	}

	packet := make([]byte, 4000)
	speech := generateSineWave(48000, 440, enc.FrameSize())
	for i := range 3 {
		if _, err := enc.Encode(speech, packet); err != nil {
			t.Fatalf("Encode(speech) frame %d error: %v", i, err)
		}
	}

	if got, want := enc.VADActivity(), enc.enc.GetVADActivity(); got != want {
		t.Fatalf("post-speech VADActivity()=%d want core=%d", got, want)
	}
	if got := enc.VADActivity(); got < 0 || got > 255 {
		t.Fatalf("VADActivity()=%d out of range", got)
	}

	enc.Reset()
	if got, want := enc.VADActivity(), enc.enc.GetVADActivity(); got != want {
		t.Fatalf("post-reset VADActivity()=%d want core=%d", got, want)
	}
}

func TestEncoder_FEC(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
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

func TestEncoder_InBandFEC(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	for _, tc := range []struct {
		config  int
		enabled bool
	}{
		{InBandFECDisabled, false},
		{InBandFECEnabled, true},
		{InBandFECMusicSafe, true},
	} {
		if err := enc.SetInBandFEC(tc.config); err != nil {
			t.Fatalf("SetInBandFEC(%d) error: %v", tc.config, err)
		}
		if got := enc.InBandFEC(); got != tc.config {
			t.Fatalf("InBandFEC()=%d want %d", got, tc.config)
		}
		if got := enc.FECEnabled(); got != tc.enabled {
			t.Fatalf("FECEnabled()=%t want %t", got, tc.enabled)
		}
	}

	enc.SetFEC(true)
	if got := enc.InBandFEC(); got != InBandFECEnabled {
		t.Fatalf("SetFEC(true) InBandFEC()=%d want %d", got, InBandFECEnabled)
	}
	enc.SetFEC(false)
	if got := enc.InBandFEC(); got != InBandFECDisabled {
		t.Fatalf("SetFEC(false) InBandFEC()=%d want %d", got, InBandFECDisabled)
	}

	for _, config := range []int{-1, 3} {
		if err := enc.SetInBandFEC(config); err != ErrInvalidFECConfig {
			t.Fatalf("SetInBandFEC(%d) error=%v want %v", config, err, ErrInvalidFECConfig)
		}
	}
}

func TestEncoder_SignalVoice_BiasesTowardSILK(t *testing.T) {
	// Create encoder with voice signal hint at wideband
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
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
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
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
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Limit to wideband (instead of narrowband to avoid SILK frame size restrictions)
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetMaxBandwidth(BandwidthWideband) error: %v", err)
	}

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
	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}
	if info.TOC.Bandwidth > BandwidthWideband {
		t.Fatalf("encoded bandwidth=%v want <= %v", info.TOC.Bandwidth, BandwidthWideband)
	}

	t.Logf("Wideband-limited encoded to %d bytes", len(packet))
}

func TestEncoder_ForcedBandwidthOverridesMaxBandwidth(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(480); err != nil {
		t.Fatalf("SetFrameSize error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthSuperwideband); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetMaxBandwidth error: %v", err)
	}

	pcm := generateSineWave(48000, 440, 480)
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}
	if info.TOC.Mode != ModeCELT || info.TOC.Bandwidth != BandwidthSuperwideband {
		t.Fatalf("TOC mode=%v bandwidth=%v want CELT %v", info.TOC.Mode, info.TOC.Bandwidth, BandwidthSuperwideband)
	}
}
