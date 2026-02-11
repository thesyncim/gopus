package gopus

import (
	"math"
	"testing"
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

	// Encode a frame after setting controls to verify no errors
	frameSize := 960
	pcm := generateSurroundTestSignal(48000, frameSize, channels)
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Errorf("Encode after controls error: %v", err)
	}
	if len(packet) == 0 {
		t.Error("Encode after controls produced empty packet")
	}

	t.Logf("Controls verified: bitrate=%d, complexity=%d, FEC=%v, DTX=%v",
		enc.Bitrate(), enc.Complexity(), enc.FECEnabled(), enc.DTXEnabled())
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
