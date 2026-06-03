package gopus

import (
	"math"
	"testing"
)

// TestMultistreamRoundTrip_51 tests 5.1 surround (6 channel) encode/decode round-trip.
func TestMultistreamRoundTrip_51(t *testing.T) {
	channels := 6 // 5.1 surround
	sampleRate := 48000
	frameSize := 960 // 20ms

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

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

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

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

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

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

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

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

// TestMultistreamRoundTrip_Int16 tests int16 encode/decode path.
func TestMultistreamRoundTrip_Int16(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

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
			enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, tc.app)
			dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

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

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

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
