package gopus

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	encodercore "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/benchutil"
)

// generateSurroundTestSignal generates a multi-channel test signal with unique frequency per channel.
// This helps verify channel routing by making each channel's content distinguishable.
func generateSurroundTestSignal(sampleRate, frameSize, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	// Use different frequencies for each channel
	// Base frequencies spaced to be distinguishable: 220, 330, 440, 550, 660, 770, 880, 990 Hz
	baseFreq := 220.0

	for s := 0; s < frameSize; s++ {
		for ch := 0; ch < channels; ch++ {
			freq := baseFreq + float64(ch)*110
			val := float32(0.3 * math.Sin(2*math.Pi*freq*float64(s)/float64(sampleRate)))
			pcm[s*channels+ch] = val
		}
	}
	return pcm
}

func generateSurroundTestSignalInt24(sampleRate, frameSize, channels int) []int32 {
	pcm := make([]int32, frameSize*channels)
	baseFreq := 220.0

	for s := 0; s < frameSize; s++ {
		for ch := 0; ch < channels; ch++ {
			freq := baseFreq + float64(ch)*110
			val := int32((1 << 22) * math.Sin(2*math.Pi*freq*float64(s)/float64(sampleRate)))
			pcm[s*channels+ch] = val
		}
	}
	return pcm
}

// computeEnergyFloat32 computes the RMS energy of a float32 signal.
func computeEnergyFloat32(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// computeChannelEnergy computes the RMS energy for a single channel in interleaved audio.
func computeChannelEnergy(samples []float32, channels, targetChannel int) float64 {
	if len(samples) == 0 || targetChannel >= channels {
		return 0
	}
	var sum float64
	var count int
	for i := targetChannel; i < len(samples); i += channels {
		sum += float64(samples[i]) * float64(samples[i])
		count++
	}
	if count == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(count))
}

// TestMultistreamEncoder_Creation tests encoder creation for various channel counts.
func TestMultistreamEncoder_Creation(t *testing.T) {
	// Test NewMultistreamEncoderDefault for channels 1-8
	for channels := 1; channels <= 8; channels++ {
		t.Run(string(rune('0'+channels))+"ch", func(t *testing.T) {
			enc, err := NewMultistreamEncoderDefault(48000, channels, ApplicationAudio)
			if err != nil {
				t.Fatalf("NewMultistreamEncoderDefault(%d channels) error: %v", channels, err)
			}

			if enc.Channels() != channels {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), channels)
			}
			if enc.SampleRate() != 48000 {
				t.Errorf("SampleRate() = %d, want 48000", enc.SampleRate())
			}

			// Verify stream counts based on channel configuration
			streams := enc.Streams()
			coupled := enc.CoupledStreams()
			t.Logf("%d channels: %d streams, %d coupled", channels, streams, coupled)

			// Sanity check: coupled <= streams
			if coupled > streams {
				t.Errorf("CoupledStreams(%d) > Streams(%d)", coupled, streams)
			}
		})
	}

	// Test invalid sample rates
	_, err := NewMultistreamEncoderDefault(44100, 6, ApplicationAudio)
	if err != ErrInvalidSampleRate {
		t.Errorf("Invalid sample rate: got error %v, want ErrInvalidSampleRate", err)
	}

	// Test invalid channels (0)
	_, err = NewMultistreamEncoderDefault(48000, 0, ApplicationAudio)
	if err != ErrInvalidChannels {
		t.Errorf("Zero channels: got error %v, want ErrInvalidChannels", err)
	}

	// Test invalid channels (>8 for default)
	_, err = NewMultistreamEncoderDefault(48000, 9, ApplicationAudio)
	if err != ErrInvalidChannels {
		t.Errorf("9 channels: got error %v, want ErrInvalidChannels", err)
	}

	_, err = NewMultistreamEncoderDefault(48000, 6, Application(99))
	if err != ErrInvalidApplication {
		t.Errorf("invalid application: got error %v, want ErrInvalidApplication", err)
	}
}

// TestMultistreamDecoder_Creation tests decoder creation for various channel counts.
func TestMultistreamDecoder_Creation(t *testing.T) {
	// Test NewMultistreamDecoderDefault for channels 1-8
	for channels := 1; channels <= 8; channels++ {
		t.Run(string(rune('0'+channels))+"ch", func(t *testing.T) {
			dec, err := NewMultistreamDecoderDefault(48000, channels)
			if err != nil {
				t.Fatalf("NewMultistreamDecoderDefault(%d channels) error: %v", channels, err)
			}

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
	if err != ErrInvalidChannels {
		t.Errorf("Zero channels: got error %v, want ErrInvalidChannels", err)
	}
}

// TestMultistreamRoundTrip_51 tests 5.1 surround (6 channel) encode/decode round-trip.
func TestMultistreamRoundTrip_51(t *testing.T) {
	channels := 6 // 5.1 surround
	sampleRate := 48000
	frameSize := 960 // 20ms

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}

	// Generate 6-channel test signal
	pcmIn := generateSurroundTestSignal(sampleRate, frameSize, channels)
	inputEnergy := computeEnergyFloat32(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encoded packet is empty")
	}

	// Decode
	pcmOut := make([]float32, frameSize*channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergyFloat32(pcmOut[:n*channels])

	// Verify output length
	expectedLen := frameSize * channels
	if n*channels != expectedLen {
		t.Errorf("Output length = %d, want %d", n*channels, expectedLen)
	}

	// Log metrics
	t.Logf("5.1 round-trip: input energy=%.4f, output energy=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))

	// Energy ratio
	if inputEnergy > 0 {
		ratio := outputEnergy / inputEnergy
		t.Logf("Energy ratio: %.2f%%", ratio*100)
	}

	// Verify non-zero output
	if outputEnergy == 0 {
		t.Error("Output has zero energy")
	}

	// Log per-channel energy
	for ch := 0; ch < channels; ch++ {
		chEnergy := computeChannelEnergy(pcmOut[:n*channels], channels, ch)
		t.Logf("  Channel %d energy: %.4f", ch, chEnergy)
	}
}

// TestMultistreamRoundTrip_71 tests 7.1 surround (8 channel) encode/decode round-trip.
func TestMultistreamRoundTrip_71(t *testing.T) {
	channels := 8 // 7.1 surround
	sampleRate := 48000
	frameSize := 960 // 20ms

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}

	// Generate 8-channel test signal
	pcmIn := generateSurroundTestSignal(sampleRate, frameSize, channels)
	inputEnergy := computeEnergyFloat32(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encoded packet is empty")
	}

	// Decode
	pcmOut := make([]float32, frameSize*channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergyFloat32(pcmOut[:n*channels])

	// Verify output length
	expectedLen := frameSize * channels
	if n*channels != expectedLen {
		t.Errorf("Output length = %d, want %d", n*channels, expectedLen)
	}

	// Log metrics
	t.Logf("7.1 round-trip: input energy=%.4f, output energy=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))

	// Energy ratio
	if inputEnergy > 0 {
		ratio := outputEnergy / inputEnergy
		t.Logf("Energy ratio: %.2f%%", ratio*100)
	}

	// Verify non-zero output
	if outputEnergy == 0 {
		t.Error("Output has zero energy")
	}
}

// TestMultistreamRoundTrip_Stereo tests stereo (2 channel) multistream as edge case.
func TestMultistreamRoundTrip_Stereo(t *testing.T) {
	channels := 2 // Stereo
	sampleRate := 48000
	frameSize := 960 // 20ms

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}

	// Generate stereo test signal (L: 440Hz, R: 880Hz)
	pcmIn := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		pcmIn[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
		pcmIn[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*880*float64(i)/float64(sampleRate)))
	}
	inputEnergy := computeEnergyFloat32(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Decode
	pcmOut := make([]float32, frameSize*channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergyFloat32(pcmOut[:n*channels])

	t.Logf("Stereo round-trip: input=%.4f, output=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))

	// Verify streams and coupled streams for stereo
	if enc.Streams() != 1 {
		t.Errorf("Stereo should have 1 stream, got %d", enc.Streams())
	}
	if enc.CoupledStreams() != 1 {
		t.Errorf("Stereo should have 1 coupled stream, got %d", enc.CoupledStreams())
	}
}

// TestMultistreamRoundTrip_MultipleFrames tests encoding/decoding multiple consecutive frames.
func TestMultistreamRoundTrip_MultipleFrames(t *testing.T) {
	channels := 6 // 5.1 surround
	sampleRate := 48000
	frameSize := 960 // 20ms
	numFrames := 10

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}

	var totalPacketBytes int
	var totalInputEnergy, totalOutputEnergy float64

	pcmOut := make([]float32, frameSize*channels)
	for i := 0; i < numFrames; i++ {
		// Generate unique signal for each frame
		pcmIn := generateSurroundTestSignal(sampleRate, frameSize, channels)
		// Shift frequency slightly for each frame
		for s := 0; s < frameSize*channels; s++ {
			pcmIn[s] *= float32(1.0 - float64(i)*0.05) // Slight amplitude variation
		}

		totalInputEnergy += computeEnergyFloat32(pcmIn)

		// Encode
		packet, err := enc.EncodeFloat32(pcmIn)
		if err != nil {
			t.Fatalf("Frame %d encode error: %v", i, err)
		}
		totalPacketBytes += len(packet)

		// Decode
		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			t.Fatalf("Frame %d decode error: %v", i, err)
		}
		totalOutputEnergy += computeEnergyFloat32(pcmOut[:n*channels])
	}

	avgPacketSize := totalPacketBytes / numFrames
	avgInputEnergy := totalInputEnergy / float64(numFrames)
	avgOutputEnergy := totalOutputEnergy / float64(numFrames)

	t.Logf("Multiple frames: %d frames, avg packet=%d bytes, avg input=%.4f, avg output=%.4f",
		numFrames, avgPacketSize, avgInputEnergy, avgOutputEnergy)

	if avgOutputEnergy == 0 {
		t.Error("Average output energy is zero")
	}
}

// TestMultistreamEncoder_Controls tests encoder control methods.
func TestMultistreamEncoder_Controls(t *testing.T) {
	channels := 6
	enc, err := NewMultistreamEncoderDefault(48000, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	// Test application control
	if got := enc.Application(); got != ApplicationAudio {
		t.Fatalf("Application()=%v want=%v", got, ApplicationAudio)
	}
	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(ApplicationVoIP) error: %v", err)
	}
	if got := enc.Application(); got != ApplicationVoIP {
		t.Fatalf("Application()=%v want=%v after SetApplication", got, ApplicationVoIP)
	}
	if err := enc.SetApplication(Application(-1)); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(invalid) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := enc.SetApplication(ApplicationRestrictedSilk); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(restricted silk) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := enc.SetApplication(ApplicationRestrictedCelt); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(restricted celt) error=%v want=%v", err, ErrInvalidApplication)
	}

	// Test SetBitrate
	err = enc.SetBitrate(256000)
	if err != nil {
		t.Errorf("SetBitrate(256000) error: %v", err)
	}
	if enc.Bitrate() != 256000 {
		t.Errorf("Bitrate() = %d, want 256000", enc.Bitrate())
	}

	// Test SetComplexity
	err = enc.SetComplexity(8)
	if err != nil {
		t.Errorf("SetComplexity(8) error: %v", err)
	}
	if enc.Complexity() != 8 {
		t.Errorf("Complexity() = %d, want 8", enc.Complexity())
	}

	// Test invalid complexity
	err = enc.SetComplexity(11)
	if err != ErrInvalidComplexity {
		t.Errorf("SetComplexity(11) error = %v, want ErrInvalidComplexity", err)
	}

	// Test bitrate mode controls
	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Errorf("BitrateMode() = %v, want %v by default", got, BitrateModeCVBR)
	}
	if !enc.VBR() {
		t.Error("VBR() should be true by default")
	}
	if !enc.VBRConstraint() {
		t.Error("VBRConstraint() should be true by default")
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Errorf("SetBitrateMode(BitrateModeCBR) error: %v", err)
	}
	if got := enc.BitrateMode(); got != BitrateModeCBR {
		t.Errorf("BitrateMode() = %v, want %v", got, BitrateModeCBR)
	}
	enc.SetVBR(true)
	if !enc.VBR() {
		t.Error("VBR() should be true after SetVBR(true)")
	}
	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Errorf("BitrateMode() = %v, want %v after SetVBR(true) with retained constraint", got, BitrateModeCVBR)
	}
	enc.SetVBRConstraint(true)
	if !enc.VBRConstraint() {
		t.Error("VBRConstraint() should be true after SetVBRConstraint(true)")
	}
	enc.SetVBRConstraint(false)
	if got := enc.BitrateMode(); got != BitrateModeVBR {
		t.Errorf("BitrateMode() = %v, want %v after SetVBRConstraint(false)", got, BitrateModeVBR)
	}
	enc.SetVBR(false)
	if got := enc.BitrateMode(); got != BitrateModeCBR {
		t.Errorf("BitrateMode() = %v, want %v after SetVBR(false)", got, BitrateModeCBR)
	}
	enc.SetVBR(true)
	if got := enc.BitrateMode(); got != BitrateModeVBR {
		t.Errorf("BitrateMode() = %v, want %v after re-enabling VBR with constraint=false", got, BitrateModeVBR)
	}
	if err := enc.SetBitrateMode(BitrateMode(99)); err != ErrInvalidBitrateMode {
		t.Errorf("SetBitrateMode(invalid) error = %v, want %v", err, ErrInvalidBitrateMode)
	}

	// Test SetFEC
	enc.SetFEC(true)
	if !enc.FECEnabled() {
		t.Error("FEC should be enabled")
	}
	enc.SetFEC(false)
	if enc.FECEnabled() {
		t.Error("FEC should be disabled")
	}

	// Test SetDTX
	enc.SetDTX(true)
	if !enc.DTXEnabled() {
		t.Error("DTX should be enabled")
	}
	enc.SetDTX(false)
	if enc.DTXEnabled() {
		t.Error("DTX should be disabled")
	}

	// Test SetPacketLoss
	err = enc.SetPacketLoss(15)
	if err != nil {
		t.Errorf("SetPacketLoss(15) error: %v", err)
	}
	if enc.PacketLoss() != 15 {
		t.Errorf("PacketLoss() = %d, want 15", enc.PacketLoss())
	}
	err = enc.SetPacketLoss(101)
	if err != ErrInvalidPacketLoss {
		t.Errorf("SetPacketLoss(101) error = %v, want ErrInvalidPacketLoss", err)
	}

	// Test bandwidth control
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Errorf("SetBandwidth(BandwidthWideband) error: %v", err)
	}
	if got := enc.Bandwidth(); got != BandwidthWideband {
		t.Errorf("Bandwidth() = %v, want %v", got, BandwidthWideband)
	}
	if err := enc.SetBandwidth(Bandwidth(255)); err != ErrInvalidBandwidth {
		t.Errorf("SetBandwidth(invalid) error = %v, want %v", err, ErrInvalidBandwidth)
	}
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Errorf("SetMaxBandwidth(BandwidthWideband) error: %v", err)
	}
	if got := enc.MaxBandwidth(); got != BandwidthWideband {
		t.Errorf("MaxBandwidth() = %v, want %v", got, BandwidthWideband)
	}
	if err := enc.SetMaxBandwidth(Bandwidth(255)); err != ErrInvalidBandwidth {
		t.Errorf("SetMaxBandwidth(invalid) error = %v, want %v", err, ErrInvalidBandwidth)
	}

	// Test frame size control
	if got := enc.FrameSize(); got != 960 {
		t.Errorf("FrameSize() = %d, want 960", got)
	}
	for _, size := range []int{120, 240, 480, 960, 1920, 2880, 3840, 4800, 5760} {
		if err := enc.SetFrameSize(size); err != nil {
			t.Errorf("SetFrameSize(%d) error: %v", size, err)
		}
		if got := enc.FrameSize(); got != size {
			t.Errorf("FrameSize() = %d, want %d", got, size)
		}
	}
	if err := enc.SetFrameSize(111); err != ErrInvalidFrameSize {
		t.Errorf("SetFrameSize(invalid) error = %v, want %v", err, ErrInvalidFrameSize)
	}
	if err := enc.SetFrameSize(960); err != nil {
		t.Errorf("SetFrameSize(960) error: %v", err)
	}

	if got := enc.ExpertFrameDuration(); got != ExpertFrameDurationArg {
		t.Errorf("ExpertFrameDuration() = %v, want %v", got, ExpertFrameDurationArg)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration120Ms); err != nil {
		t.Errorf("SetExpertFrameDuration(120ms) error: %v", err)
	}
	if got := enc.FrameSize(); got != 5760 {
		t.Errorf("FrameSize() after 120ms = %d, want 5760", got)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDuration120Ms {
		t.Errorf("ExpertFrameDuration() after 120ms = %v, want %v", got, ExpertFrameDuration120Ms)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDurationArg); err != nil {
		t.Errorf("SetExpertFrameDuration(arg) error: %v", err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration(0)); err != ErrInvalidArgument {
		t.Errorf("SetExpertFrameDuration(invalid) error = %v, want %v", err, ErrInvalidArgument)
	}

	// Test force channels control
	for _, ch := range []int{1, 2, -1} {
		if err := enc.SetForceChannels(ch); err != nil {
			t.Errorf("SetForceChannels(%d) error: %v", ch, err)
		}
		if got := enc.ForceChannels(); got != ch {
			t.Errorf("ForceChannels() = %d, want %d", got, ch)
		}
	}
	if err := enc.SetForceChannels(0); err != ErrInvalidForceChannels {
		t.Errorf("SetForceChannels(0) error = %v, want %v", err, ErrInvalidForceChannels)
	}

	// Test prediction and phase inversion controls
	enc.SetPredictionDisabled(true)
	if !enc.PredictionDisabled() {
		t.Error("PredictionDisabled() should be true after SetPredictionDisabled(true)")
	}
	enc.SetPredictionDisabled(false)
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() should be false after SetPredictionDisabled(false)")
	}

	enc.SetPhaseInversionDisabled(true)
	if !enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() should be true after SetPhaseInversionDisabled(true)")
	}
	enc.SetPhaseInversionDisabled(false)
	if enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() should be false after SetPhaseInversionDisabled(false)")
	}

	// Test signal hint control parity (libopus OPUS_SET_SIGNAL semantics).
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Errorf("SetSignal(SignalVoice) error: %v", err)
	}
	if got := enc.Signal(); got != SignalVoice {
		t.Errorf("Signal() = %v, want %v", got, SignalVoice)
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Errorf("SetSignal(SignalMusic) error: %v", err)
	}
	if got := enc.Signal(); got != SignalMusic {
		t.Errorf("Signal() = %v, want %v", got, SignalMusic)
	}
	if err := enc.SetSignal(Signal(9999)); err != ErrInvalidSignal {
		t.Errorf("SetSignal(invalid) error = %v, want %v", err, ErrInvalidSignal)
	}

	// Encode a frame after setting controls to verify no errors
	frameSize := enc.FrameSize()
	pcm := generateSurroundTestSignal(48000, frameSize, channels)
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Errorf("Encode after controls error: %v", err)
	}
	if len(packet) == 0 {
		t.Error("Encode after controls produced empty packet")
	}
	if enc.FinalRange() != enc.GetFinalRange() {
		t.Errorf("FinalRange() = %d, want %d", enc.FinalRange(), enc.GetFinalRange())
	}

	t.Logf("Controls verified: app=%v bitrate=%d complexity=%d mode=%v FEC=%v DTX=%v",
		enc.Application(), enc.Bitrate(), enc.Complexity(), enc.BitrateMode(), enc.FECEnabled(), enc.DTXEnabled())
}

func TestMultistreamEncoder_EncodeInt24(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 6, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	pcm := generateSurroundTestSignalInt24(48000, enc.FrameSize(), enc.Channels())
	data := make([]byte, 4000*enc.Streams())

	n, err := enc.EncodeInt24(pcm, data)
	if err != nil {
		t.Fatalf("EncodeInt24 error: %v", err)
	}
	if n == 0 {
		t.Fatal("EncodeInt24 returned 0 bytes")
	}

	packet, err := enc.EncodeInt24Slice(pcm)
	if err != nil {
		t.Fatalf("EncodeInt24Slice error: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("EncodeInt24Slice returned empty packet")
	}
}

func TestMultistreamEncoder_EncodeInt24InvalidFrameSize(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 6, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	short := make([]int32, enc.FrameSize()*enc.Channels()-1)
	data := make([]byte, 4000*enc.Streams())
	if _, err := enc.EncodeInt24(short, data); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeInt24(short) error=%v want=%v", err, ErrInvalidFrameSize)
	}
	if _, err := enc.EncodeInt24Slice(short); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeInt24Slice(short) error=%v want=%v", err, ErrInvalidFrameSize)
	}
}

func TestMultistreamEncoder_CVBRPacketEnvelope(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 6, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Fatalf("BitrateMode() = %v, want %v", got, BitrateModeCVBR)
	}

	frameSize := 960
	pcm := generateSurroundTestSignal(48000, frameSize, 6)
	data := make([]byte, 4000*enc.Streams())

	for _, bitrate := range []int{128000, 256000, 384000} {
		if err := enc.SetBitrateMode(BitrateModeCVBR); err != nil {
			t.Fatalf("SetBitrateMode(CVBR) error: %v", err)
		}
		if err := enc.SetBitrate(bitrate); err != nil {
			t.Fatalf("SetBitrate(%d) error: %v", bitrate, err)
		}
		enc.Reset()

		maxPacket := 0
		for i := 0; i < 10; i++ {
			n, err := enc.Encode(pcm, data)
			if err != nil {
				t.Fatalf("Encode bitrate=%d frame=%d error: %v", bitrate, i, err)
			}
			if n > maxPacket {
				maxPacket = n
			}
		}
		if maxPacket > 1275 {
			t.Fatalf("bitrate=%d max packet=%d exceeds 1275-byte envelope", bitrate, maxPacket)
		}
	}
}

// TestMultistreamEncoder_SetApplicationPreservesControls verifies application
// updates do not clobber other encoder CTLs.
func TestMultistreamEncoder_SetApplicationPreservesControls(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 6, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	const wantBitrate = 210000
	const wantComplexity = 3

	if err := enc.SetBitrate(wantBitrate); err != nil {
		t.Fatalf("SetBitrate(%d) error: %v", wantBitrate, err)
	}
	if err := enc.SetComplexity(wantComplexity); err != nil {
		t.Fatalf("SetComplexity(%d) error: %v", wantComplexity, err)
	}

	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(ApplicationVoIP) error: %v", err)
	}

	if got := enc.Bitrate(); got != wantBitrate {
		t.Fatalf("Bitrate() after SetApplication = %d, want %d", got, wantBitrate)
	}
	if got := enc.Complexity(); got != wantComplexity {
		t.Fatalf("Complexity() after SetApplication = %d, want %d", got, wantComplexity)
	}
}

func TestMultistreamEncoder_SetApplicationForwardsModeAndBandwidth(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 6, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	if got := enc.enc.Mode(); got != encodercore.ModeAuto {
		t.Fatalf("initial Mode() = %v, want %v", got, encodercore.ModeAuto)
	}
	if enc.enc.LowDelay() {
		t.Fatalf("initial LowDelay() = true, want false")
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("initial Bandwidth() = %v, want %v", got, BandwidthFullband)
	}

	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(ApplicationVoIP) error: %v", err)
	}
	if got := enc.enc.Mode(); got != encodercore.ModeAuto {
		t.Fatalf("Mode() after VoIP = %v, want %v", got, encodercore.ModeAuto)
	}
	if enc.enc.LowDelay() {
		t.Fatalf("LowDelay() after VoIP = true, want false")
	}
	if got := enc.Bandwidth(); got != BandwidthWideband {
		t.Fatalf("Bandwidth() after VoIP = %v, want %v", got, BandwidthWideband)
	}

	if err := enc.SetApplication(ApplicationLowDelay); err != nil {
		t.Fatalf("SetApplication(ApplicationLowDelay) error: %v", err)
	}
	if got := enc.enc.Mode(); got != encodercore.ModeCELT {
		t.Fatalf("Mode() after LowDelay = %v, want %v", got, encodercore.ModeCELT)
	}
	if !enc.enc.LowDelay() {
		t.Fatalf("LowDelay() after LowDelay app = false, want true")
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth() after LowDelay = %v, want %v", got, BandwidthFullband)
	}
}

func TestMultistreamEncoder_SetApplicationAfterEncodeRejected(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 6, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	pcm := generateSurroundTestSignal(48000, 960, 6)
	packet := make([]byte, 4000*enc.Streams())
	assertApplicationLockAfterEncode(
		t,
		enc.Application,
		enc.SetApplication,
		enc.Reset,
		func() error {
			_, err := enc.Encode(pcm, packet)
			return err
		},
		ApplicationVoIP,
		ApplicationVoIP,
	)
}

func TestMultistreamEncoder_RestrictedApplications(t *testing.T) {
	for _, tt := range restrictedApplicationTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewMultistreamEncoderDefault(48000, 6, tt.application)
			if err != nil {
				t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
			}

			if got := enc.Application(); got != tt.application {
				t.Fatalf("Application()=%v want=%v", got, tt.application)
			}
			if got := enc.enc.Mode(); got != tt.wantMode {
				t.Fatalf("Mode()=%v want=%v", got, tt.wantMode)
			}
			if got := enc.enc.LowDelay(); got != tt.wantLowDelay {
				t.Fatalf("LowDelay()=%v want=%v", got, tt.wantLowDelay)
			}
			if got := enc.Bandwidth(); got != tt.wantBandwidth {
				t.Fatalf("Bandwidth()=%v want=%v", got, tt.wantBandwidth)
			}
			if got := enc.Lookahead(); got != tt.wantLookahead {
				t.Fatalf("Lookahead()=%d want=%d", got, tt.wantLookahead)
			}

			if err := enc.SetApplication(tt.application); err != ErrInvalidApplication {
				t.Fatalf("SetApplication(same restricted) error=%v want=%v", err, ErrInvalidApplication)
			}
			if err := enc.SetApplication(ApplicationAudio); err != ErrInvalidApplication {
				t.Fatalf("SetApplication(change restricted) error=%v want=%v", err, ErrInvalidApplication)
			}
			if tt.application == ApplicationRestrictedSilk {
				if err := enc.SetFrameSize(240); err != ErrInvalidFrameSize {
					t.Fatalf("SetFrameSize(240) error=%v want=%v", err, ErrInvalidFrameSize)
				}
				if err := enc.SetFrameSize(480); err != nil {
					t.Fatalf("SetFrameSize(480) error: %v", err)
				}
			}
		})
	}
}

func TestMultistreamDecoder_IgnoreExtensions(t *testing.T) {
	dec, err := NewMultistreamDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}

	assertIgnoreExtensionsControls(t, dec)
}

func TestMultistreamEncoder_OptionalExtensionControls(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	assertOptionalEncoderControls(t, enc)
	assertSupportedQEXTControl(t, enc)
}

func TestMultistreamDecoder_OptionalExtensionControls(t *testing.T) {
	dec, err := NewMultistreamDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}

	assertOptionalDecoderControls(t, dec)
}

func TestMultistreamDNNBlobPropagatesToInternalState(t *testing.T) {
	enc, err := NewMultistreamEncoderDefault(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault encoder error: %v", err)
	}
	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("encoder SetDNNBlob error: %v", err)
	}
	if enc.dnnBlob == nil {
		t.Fatal("encoder wrapper dnnBlob=nil want non-nil")
	}
	if !enc.enc.DNNBlobLoaded() {
		t.Fatal("internal multistream encoder DNNBlobLoaded()=false want true")
	}
	if !enc.enc.DREDModelLoaded() {
		t.Fatal("internal multistream encoder DREDModelLoaded()=false want true")
	}
	if enc.enc.DREDReady() {
		t.Fatal("internal multistream encoder DREDReady()=true without dred duration")
	}
	enc.Reset()
	if !enc.enc.DNNBlobLoaded() {
		t.Fatal("internal multistream encoder DNNBlobLoaded()=false after Reset")
	}
	if !enc.enc.DREDModelLoaded() {
		t.Fatal("internal multistream encoder lost DRED model across Reset")
	}

	dec, err := NewMultistreamDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault decoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("decoder SetDNNBlob error: %v", err)
	}
	if dec.dnnBlob == nil {
		t.Fatal("decoder wrapper dnnBlob=nil want non-nil")
	}
	if !dec.dec.DNNBlobLoaded() {
		t.Fatal("internal multistream decoder DNNBlobLoaded()=false want true")
	}
	if !dec.dec.PitchDNNLoaded() {
		t.Fatal("internal multistream decoder PitchDNNLoaded()=false want true")
	}
	if !dec.dec.PLCModelLoaded() {
		t.Fatal("internal multistream decoder PLCModelLoaded()=false want true")
	}
	if !dec.dec.FARGANModelLoaded() {
		t.Fatal("internal multistream decoder FARGANModelLoaded()=false want true")
	}
	if dec.dec.DREDModelLoaded() {
		t.Fatal("internal multistream decoder DREDModelLoaded()=true without DRED decoder model")
	}
	if !dec.dec.OSCEModelsLoaded() {
		t.Fatal("internal multistream decoder OSCEModelsLoaded()=false want true")
	}
	if dec.dec.OSCEBWEModelLoaded() {
		t.Fatal("internal multistream decoder OSCEBWEModelLoaded()=true without OSCE_BWE model")
	}
	dec.Reset()
	if !dec.dec.DNNBlobLoaded() {
		t.Fatal("internal multistream decoder DNNBlobLoaded()=false after Reset")
	}
	if !dec.dec.PitchDNNLoaded() || !dec.dec.PLCModelLoaded() || !dec.dec.FARGANModelLoaded() || !dec.dec.OSCEModelsLoaded() {
		t.Fatal("internal multistream decoder lost core model readiness across Reset")
	}
	if dec.dec.DREDModelLoaded() || dec.dec.OSCEBWEModelLoaded() {
		t.Fatal("internal multistream decoder gained unsupported model readiness across Reset")
	}
}

func TestMultistreamDecoderDREDBlobPropagatesCapability(t *testing.T) {
	dec, err := NewMultistreamDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault decoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("decoder SetDNNBlob error: %v", err)
	}
	if !dec.dec.DREDModelLoaded() {
		t.Fatal("internal multistream decoder DREDModelLoaded()=false want true")
	}

	dec.Reset()
	if !dec.dec.DREDModelLoaded() {
		t.Fatal("internal multistream decoder lost DRED model across Reset")
	}
}

func TestMultistreamDecoderUnsupportedQEXTExtensionMatchesBasePacket(t *testing.T) {
	if SupportsOptionalExtension(OptionalExtensionQEXT) {
		t.Skip("test covers the default unsupported-QEXT build")
	}

	enc, err := NewMultistreamEncoderDefault(48000, 2, ApplicationLowDelay)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}
	if err := enc.SetBitrate(256000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	pcm := generateSurroundTestSignal(48000, enc.FrameSize(), enc.Channels())
	base, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}
	toc, _, err := packetFrameCount(base)
	if err != nil {
		t.Fatalf("packetFrameCount error: %v", err)
	}
	if toc.Mode != ModeCELT {
		t.Fatalf("encoded mode=%v want %v", toc.Mode, ModeCELT)
	}

	extended := buildSingleFramePacketWithExtensionsForTest(t, base, []packetExtensionData{
		{ID: qextPacketExtensionID, Frame: 0, Data: []byte{0x51, 0x8C, 0xE0}},
	})

	for _, ignore := range []bool{false, true} {
		baseDec, err := NewMultistreamDecoderDefault(48000, 2)
		if err != nil {
			t.Fatalf("NewMultistreamDecoderDefault(base) error: %v", err)
		}
		baseDec.SetIgnoreExtensions(ignore)
		basePCM := make([]float32, 960*2)
		baseN, err := baseDec.Decode(base, basePCM)
		if err != nil {
			t.Fatalf("Decode(base, ignore=%v): %v", ignore, err)
		}

		extDec, err := NewMultistreamDecoderDefault(48000, 2)
		if err != nil {
			t.Fatalf("NewMultistreamDecoderDefault(extended) error: %v", err)
		}
		extDec.SetIgnoreExtensions(ignore)
		extPCM := make([]float32, 960*2)
		extN, err := extDec.Decode(extended, extPCM)
		if err != nil {
			t.Fatalf("Decode(extended, ignore=%v): %v", ignore, err)
		}

		if extN != baseN {
			t.Fatalf("Decode sample count=%d want %d (ignore=%v)", extN, baseN, ignore)
		}
		for i := 0; i < extN*2; i++ {
			if extPCM[i] != basePCM[i] {
				t.Fatalf("sample[%d]=%v want %v (ignore=%v)", i, extPCM[i], basePCM[i], ignore)
			}
		}
	}
}

func TestMultistreamEncoderDefaultQEXTPacketLibopusDecode(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		t.Skipf("QEXT-enabled opus_demo unavailable: %v", err)
	}

	enc, err := NewMultistreamEncoderDefault(48000, 2, ApplicationLowDelay)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}
	if err := enc.SetBitrate(256000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetQEXT(true); err != nil {
		t.Fatalf("SetQEXT(true) error: %v", err)
	}

	pcm := generateSurroundTestSignal(48000, enc.FrameSize(), enc.Channels())
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}

	info, frames, padding, nbFrames, err := parsePacketFramesAndPadding(packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frame count=%d want 1", len(frames))
	}
	if info.Padding == 0 {
		t.Fatal("multistream default 2ch packet missing extension padding")
	}
	ext, ok, err := findPacketExtension(padding, nbFrames, qextPacketExtensionID)
	if err != nil {
		t.Fatalf("findPacketExtension: %v", err)
	}
	if !ok || len(ext.Data) == 0 {
		t.Fatal("multistream default 2ch packet missing QEXT payload")
	}

	tmpDir := t.TempDir()
	bitstreamPath := filepath.Join(tmpDir, "qext.bit")
	outputPath := filepath.Join(tmpDir, "qext.raw")
	if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, [][]byte{packet}, 1); err != nil {
		t.Fatalf("WriteRepeatedOpusDemoBitstream: %v", err)
	}

	cmd := exec.Command(opusDemo, "-d", "48000", "2", bitstreamPath, outputPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opus_demo decode failed: %v (%s)", err, bytes.TrimSpace(out))
	}
	infoOut, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat decoded output: %v", err)
	}
	if infoOut.Size() == 0 {
		t.Fatal("opus_demo produced empty decoded output")
	}
}

// TestMultistreamDecoder_PLC tests packet loss concealment.
func TestMultistreamDecoder_PLC(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}

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

// TestMultistreamRoundTrip_Int16 tests int16 encode/decode path.
func TestMultistreamRoundTrip_Int16(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}

	// Generate int16 test signal
	pcmIn := make([]int16, frameSize*channels)
	for s := 0; s < frameSize; s++ {
		for ch := 0; ch < channels; ch++ {
			freq := 220.0 + float64(ch)*110
			pcmIn[s*channels+ch] = int16(8192 * math.Sin(2*math.Pi*freq*float64(s)/float64(sampleRate)))
		}
	}

	// Encode
	packet, err := enc.EncodeInt16Slice(pcmIn)
	if err != nil {
		t.Fatalf("EncodeInt16Slice error: %v", err)
	}

	// Decode
	pcmOut := make([]int16, frameSize*channels)
	n, err := dec.DecodeInt16(packet, pcmOut)
	if err != nil {
		t.Fatalf("DecodeInt16 error: %v", err)
	}

	// Verify output length
	expectedLen := frameSize * channels
	if n*channels != expectedLen {
		t.Errorf("Output length = %d, want %d", n*channels, expectedLen)
	}

	t.Logf("Int16 round-trip: %d input samples -> %d bytes -> %d output samples",
		len(pcmIn), len(packet), n*channels)
}

// TestMultistreamEncoder_Reset tests encoder reset functionality.
func TestMultistreamEncoder_Reset(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	// Encode a few frames
	for i := 0; i < 3; i++ {
		pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
		_, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Pre-reset encode %d error: %v", i, err)
		}
	}

	// Reset
	enc.Reset()

	// Encode more frames after reset
	for i := 0; i < 3; i++ {
		pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Post-reset encode %d error: %v", i, err)
		}
		if len(packet) == 0 {
			t.Errorf("Post-reset encode %d produced empty packet", i)
		}
	}

	t.Log("Encoder reset verified: no crashes, encoding continues normally")
}

// TestMultistreamDecoder_Reset tests decoder reset functionality.
func TestMultistreamDecoder_Reset(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}

	// Decode a few frames
	pcmOut := make([]float32, frameSize*channels)
	for i := 0; i < 3; i++ {
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
	for i := 0; i < 3; i++ {
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

// TestMultistreamEncoder_ExplicitConstructor tests explicit encoder constructor with custom mapping.
func TestMultistreamEncoder_ExplicitConstructor(t *testing.T) {
	// Test creating encoder with explicit parameters (5.1 surround)
	sampleRate := 48000
	channels := 6
	streams := 4
	coupledStreams := 2
	mapping := []byte{0, 4, 1, 2, 3, 5} // Standard 5.1 mapping

	enc, err := NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams, mapping, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoder error: %v", err)
	}

	if enc.Channels() != channels {
		t.Errorf("Channels() = %d, want %d", enc.Channels(), channels)
	}
	if enc.Streams() != streams {
		t.Errorf("Streams() = %d, want %d", enc.Streams(), streams)
	}
	if enc.CoupledStreams() != coupledStreams {
		t.Errorf("CoupledStreams() = %d, want %d", enc.CoupledStreams(), coupledStreams)
	}

	// Verify encoding works
	frameSize := 960
	pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if len(packet) == 0 {
		t.Error("Explicit constructor encoder produced empty packet")
	}

	t.Logf("Explicit constructor: %d channels, %d streams, %d coupled, packet=%d bytes",
		channels, streams, coupledStreams, len(packet))
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

// TestMultistreamRoundTrip_AllApplications tests all application modes.
func TestMultistreamRoundTrip_AllApplications(t *testing.T) {
	apps := []struct {
		app  Application
		name string
	}{
		{ApplicationVoIP, "VoIP"},
		{ApplicationAudio, "Audio"},
		{ApplicationLowDelay, "LowDelay"},
	}

	channels := 6
	sampleRate := 48000
	frameSize := 960

	for _, tc := range apps {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewMultistreamEncoderDefault(sampleRate, channels, tc.app)
			if err != nil {
				t.Fatalf("NewMultistreamEncoderDefault(%s) error: %v", tc.name, err)
			}

			dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
			if err != nil {
				t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
			}

			pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
			packet, err := enc.EncodeFloat32(pcm)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}

			pcmOut := make([]float32, frameSize*channels)
			n, err := dec.Decode(packet, pcmOut)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}

			energy := computeEnergyFloat32(pcmOut[:n*channels])
			t.Logf("%s: packet=%d bytes, output energy=%.4f", tc.name, len(packet), energy)
		})
	}
}

func TestMultistreamEncoder_Lookahead(t *testing.T) {
	for _, tc := range lookaheadTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewMultistreamEncoderDefault(tc.sampleRate, 6, tc.application)
			if err != nil {
				t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
			}
			if got := enc.Lookahead(); got != tc.want {
				t.Fatalf("Lookahead() = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("set_application_updates_lookahead_before_encode", func(t *testing.T) {
		enc, err := NewMultistreamEncoderDefault(48000, 6, ApplicationAudio)
		if err != nil {
			t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
		}
		assertLookaheadUpdatesBeforeEncode(t, enc.Lookahead, enc.SetApplication)
	})
}

// TestMultistreamRoundTrip_Mono tests mono (1 channel) multistream as edge case.
func TestMultistreamRoundTrip_Mono(t *testing.T) {
	channels := 1
	sampleRate := 48000
	frameSize := 960

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}

	// Generate mono test signal
	pcmIn := make([]float32, frameSize)
	for i := 0; i < frameSize; i++ {
		pcmIn[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
	}
	inputEnergy := computeEnergyFloat32(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Decode
	pcmOut := make([]float32, frameSize*channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergyFloat32(pcmOut[:n*channels])

	t.Logf("Mono multistream: input=%.4f, output=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))

	// Verify streams for mono
	if enc.Streams() != 1 {
		t.Errorf("Mono should have 1 stream, got %d", enc.Streams())
	}
	if enc.CoupledStreams() != 0 {
		t.Errorf("Mono should have 0 coupled streams, got %d", enc.CoupledStreams())
	}
}
