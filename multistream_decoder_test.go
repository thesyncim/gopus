package gopus

import (
	"errors"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
)

// TestMultistreamDecoder_Creation tests decoder creation for various channel counts.
func TestMultistreamDecoder_Creation(t *testing.T) {
	// Test NewMultistreamDecoderDefault for channels 1-8
	for channels := 1; channels <= 8; channels++ {
		t.Run(string(rune('0'+channels))+"ch", func(t *testing.T) {
			dec := mustNewDefaultMultistreamDecoder(t, 48000, channels)

			if dec.Channels() != channels {
				t.Errorf("Channels() = %d, want %d", dec.Channels(), channels)
			}
			if dec.SampleRate() != 48000 {
				t.Errorf("SampleRate() = %d, want 48000", dec.SampleRate())
			}

			streams := dec.Streams()
			coupled := dec.CoupledStreams()
			t.Logf("%d channels: %d streams, %d coupled", channels, streams, coupled)
		})
	}

	// Test invalid sample rates
	_, err := NewMultistreamDecoderDefault(44100, 6)
	if err != ErrInvalidSampleRate {
		t.Errorf("Invalid sample rate: got error %v, want ErrInvalidSampleRate", err)
	}

	// Test invalid channels
	_, err = NewMultistreamDecoderDefault(48000, 0)
	if !errors.Is(err, ErrInvalidChannels) {
		t.Errorf("Zero channels: got error %v, want ErrInvalidChannels", err)
	}
}

func TestMultistreamDecoder_IgnoreExtensions(t *testing.T) {
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 2)

	assertIgnoreExtensionsControls(t, dec)
}

func TestMultistreamDecoder_OptionalExtensionControls(t *testing.T) {
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 2)

	assertOptionalDecoderControls(t, dec)
	if osce, ok := any(dec).(extraOSCEBWEControl); ok {
		if extsupport.OSCEBWERuntime {
			assertWorkingOSCEBWEControl(t, osce)
		} else {
			t.Fatal("non-OSCE-runtime build unexpectedly exposes OSCE BWE control")
		}
	} else if extsupport.OSCEBWERuntime {
		t.Fatal("OSCE runtime build does not expose OSCE BWE control")
	}
}

// TestMultistreamDecoder_PLC tests packet loss concealment.
func TestMultistreamDecoder_PLC(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	// Encode and decode first frame (establishes state)
	pcm1 := generateSurroundTestSignal(sampleRate, frameSize, channels)
	packet1, err := enc.EncodeFloat32(pcm1)
	if err != nil {
		t.Fatalf("First encode error: %v", err)
	}
	pcmOut := make([]float32, frameSize*channels)
	_, err = dec.Decode(packet1, pcmOut)
	if err != nil {
		t.Fatalf("First decode error: %v", err)
	}

	// Simulate packet loss - call Decode(nil, ...) for PLC
	n, err := dec.Decode(nil, pcmOut)
	if err != nil {
		t.Fatalf("PLC decode error: %v", err)
	}

	plcEnergy := computeEnergyFloat32(pcmOut[:n*channels])

	// PLC should produce some audio
	t.Logf("PLC: %d samples, energy=%.4f", n*channels, plcEnergy)

	// Verify output length
	expectedLen := frameSize * channels
	if n*channels != expectedLen {
		t.Errorf("PLC output length = %d, want %d", n*channels, expectedLen)
	}
}

// TestMultistreamDecoder_Reset tests decoder reset functionality.
func TestMultistreamDecoder_Reset(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	// Decode a few frames
	pcmOut := make([]float32, frameSize*channels)
	for i := range 3 {
		pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
		packet, _ := enc.EncodeFloat32(pcm)
		_, err := dec.Decode(packet, pcmOut)
		if err != nil {
			t.Fatalf("Pre-reset decode %d error: %v", i, err)
		}
	}

	// Reset
	dec.Reset()

	// Decode more frames after reset
	for i := range 3 {
		pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
		packet, _ := enc.EncodeFloat32(pcm)
		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			t.Fatalf("Post-reset decode %d error: %v", i, err)
		}
		if n == 0 {
			t.Errorf("Post-reset decode %d produced empty output", i)
		}
	}

	t.Log("Decoder reset verified: no crashes, decoding continues normally")
}

func TestMultistreamDecoder_Controls(t *testing.T) {
	mono := mustNewDefaultMultistreamDecoder(t, 48000, 1)
	if !mono.PhaseInversionDisabled() {
		t.Fatal("mono PhaseInversionDisabled()=false want true")
	}
	mono.SetPhaseInversionDisabled(false)
	mono.Reset()
	if mono.PhaseInversionDisabled() {
		t.Fatal("mono Reset changed phase inversion control")
	}

	stereo := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if stereo.PhaseInversionDisabled() {
		t.Fatal("stereo PhaseInversionDisabled()=true want false")
	}
	stereo.SetPhaseInversionDisabled(true)
	stereo.Reset()
	if !stereo.PhaseInversionDisabled() {
		t.Fatal("stereo Reset changed phase inversion control")
	}

	if err := stereo.SetGain(256); err != nil {
		t.Fatalf("SetGain(256) error: %v", err)
	}
	if got := stereo.Gain(); got != 256 {
		t.Fatalf("Gain()=%d want 256", got)
	}
	if err := stereo.SetGain(32768); err != ErrInvalidGain {
		t.Fatalf("SetGain(32768) error=%v want %v", err, ErrInvalidGain)
	}
	if got := stereo.Gain(); got != 256 {
		t.Fatalf("invalid SetGain changed Gain() to %d", got)
	}
	if got := stereo.Complexity(); got != 0 {
		t.Fatalf("Complexity() default=%d want 0", got)
	}
	if err := stereo.SetComplexity(6); err != nil {
		t.Fatalf("SetComplexity(6) error: %v", err)
	}
	if got := stereo.Complexity(); got != 6 {
		t.Fatalf("Complexity()=%d want 6", got)
	}
	if err := stereo.SetComplexity(-1); err != ErrInvalidComplexity {
		t.Fatalf("SetComplexity(-1) error=%v want %v", err, ErrInvalidComplexity)
	}
	if got := stereo.Complexity(); got != 6 {
		t.Fatalf("invalid SetComplexity changed Complexity() to %d", got)
	}
	stereo.Reset()
	if got := stereo.Complexity(); got != 6 {
		t.Fatalf("Reset changed Complexity() to %d", got)
	}
	if got := stereo.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth()=%v want %v", got, BandwidthFullband)
	}
	if got := stereo.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration()=%d want 0", got)
	}
	if got := stereo.FinalRange(); got != 0 {
		t.Fatalf("FinalRange()=0x%08x before decode, want 0", got)
	}
	if got := stereo.GetFinalRange(); got != stereo.FinalRange() {
		t.Fatalf("GetFinalRange()=0x%08x FinalRange()=0x%08x", got, stereo.FinalRange())
	}
}

// TestMultistreamDecoder_ExplicitConstructor tests explicit decoder constructor with custom mapping.
func TestMultistreamDecoder_ExplicitConstructor(t *testing.T) {
	// Test creating decoder with explicit parameters (5.1 surround)
	sampleRate := 48000
	channels := 6
	streams := 4
	coupledStreams := 2
	mapping := []byte{0, 4, 1, 2, 3, 5} // Standard 5.1 mapping

	dec, err := NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		t.Fatalf("NewMultistreamDecoder error: %v", err)
	}

	if dec.Channels() != channels {
		t.Errorf("Channels() = %d, want %d", dec.Channels(), channels)
	}
	if dec.Streams() != streams {
		t.Errorf("Streams() = %d, want %d", dec.Streams(), streams)
	}
	if dec.CoupledStreams() != coupledStreams {
		t.Errorf("CoupledStreams() = %d, want %d", dec.CoupledStreams(), coupledStreams)
	}

	t.Logf("Explicit constructor decoder: %d channels, %d streams, %d coupled",
		channels, streams, coupledStreams)
}
