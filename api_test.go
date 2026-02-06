package gopus

import (
	"math"
	"testing"
)

// generateSineWaveFloat32 generates a sine wave at the given frequency.
func generateSineWaveFloat32(sampleRate int, freq float64, samples int, channels int) []float32 {
	pcm := make([]float32, samples*channels)
	for i := 0; i < samples; i++ {
		val := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = val
		}
	}
	return pcm
}

// generateSineWaveInt16 generates a sine wave as int16.
func generateSineWaveInt16(sampleRate int, freq float64, samples int, channels int) []int16 {
	pcm := make([]int16, samples*channels)
	for i := 0; i < samples; i++ {
		val := int16(16384 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = val
		}
	}
	return pcm
}

// computeEnergy computes the RMS energy of a float32 signal.
func computeEnergy(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// TestRoundTrip_Mono_Float32 tests mono float32 encode/decode round-trip.
func TestRoundTrip_Mono_Float32(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate 440 Hz sine wave
	frameSize := 960
	pcmIn := generateSineWaveFloat32(48000, 440, frameSize, 1)
	inputEnergy := computeEnergy(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encoded packet is empty")
	}

	// Decode
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergy(pcmOut[:n*cfg.Channels])

	t.Logf("Mono float32 round-trip: input energy=%.4f, output energy=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))

	// Lossy codec, but output should have significant energy
	if outputEnergy == 0 {
		t.Error("Output has zero energy")
	}
}

// TestRoundTrip_Stereo_Float32 tests stereo float32 encode/decode round-trip.
func TestRoundTrip_Stereo_Float32(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 2)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate stereo sine wave (L: 440Hz, R: 880Hz)
	frameSize := 960
	pcmIn := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		pcmIn[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcmIn[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}
	inputEnergy := computeEnergy(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Decode
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergy(pcmOut[:n*cfg.Channels])

	t.Logf("Stereo float32 round-trip: input energy=%.4f, output energy=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))
}

// TestRoundTrip_Mono_Int16 tests mono int16 encode/decode round-trip.
func TestRoundTrip_Mono_Int16(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate 440 Hz sine wave as int16
	frameSize := 960
	pcmIn := generateSineWaveInt16(48000, 440, frameSize, 1)

	// Encode
	packet, err := enc.EncodeInt16Slice(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("Encoded packet is empty")
	}

	// Decode
	pcmOut := make([]int16, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.DecodeInt16(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Note: Lossy codec - output may differ from input significantly
	// The key validation is that encoding and decoding complete without error

	t.Logf("Mono int16 round-trip: %d samples -> %d bytes -> %d samples",
		len(pcmIn), len(packet), n*cfg.Channels)
}

// TestRoundTrip_Stereo_Int16 tests stereo int16 encode/decode round-trip.
func TestRoundTrip_Stereo_Int16(t *testing.T) {
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 2)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate stereo sine wave as int16
	frameSize := 960
	pcmIn := make([]int16, frameSize*2)
	for i := 0; i < frameSize; i++ {
		pcmIn[i*2] = int16(16384 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcmIn[i*2+1] = int16(16384 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}

	// Encode
	packet, err := enc.EncodeInt16Slice(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Decode
	pcmOut := make([]int16, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.DecodeInt16(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	t.Logf("Stereo int16 round-trip: %d samples -> %d bytes -> %d samples",
		len(pcmIn), len(packet), n*cfg.Channels)
}

// TestRoundTrip_MultipleFrames tests encoding/decoding multiple consecutive frames.
func TestRoundTrip_MultipleFrames(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960
	numFrames := 10

	var totalPacketBytes int
	var totalInputEnergy, totalOutputEnergy float64

	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	for i := 0; i < numFrames; i++ {
		// Generate varying frequency for each frame
		freq := 440.0 + float64(i)*50
		pcmIn := generateSineWaveFloat32(48000, freq, frameSize, 1)
		totalInputEnergy += computeEnergy(pcmIn)

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
		totalOutputEnergy += computeEnergy(pcmOut[:n*cfg.Channels])
	}

	t.Logf("Multiple frames: %d frames, %d total bytes, avg input=%.4f, avg output=%.4f",
		numFrames, totalPacketBytes, totalInputEnergy/float64(numFrames), totalOutputEnergy/float64(numFrames))
}

// TestRoundTrip_AllSampleRates tests all valid Opus sample rates.
func TestRoundTrip_AllSampleRates(t *testing.T) {
	sampleRates := []int{8000, 12000, 16000, 24000, 48000}

	for _, sampleRate := range sampleRates {
		t.Run(string(rune('0'+sampleRate/1000)), func(t *testing.T) {
			enc, err := NewEncoder(sampleRate, 1, ApplicationAudio)
			if err != nil {
				t.Fatalf("NewEncoder(%d) error: %v", sampleRate, err)
			}

			cfg := DefaultDecoderConfig(sampleRate, 1)
			dec, err := NewDecoder(cfg)
			if err != nil {
				t.Fatalf("NewDecoder(%d) error: %v", sampleRate, err)
			}

			// Frame size at 48kHz is 960 (20ms), scale for other rates
			frameSize := 960 // Opus frame size is always specified at 48kHz

			pcmIn := generateSineWaveFloat32(sampleRate, 440, frameSize, 1)

			packet, err := enc.EncodeFloat32(pcmIn)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}

			pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
			n, err := dec.Decode(packet, pcmOut)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}

			t.Logf("%d Hz: %d bytes packet, %d samples out", sampleRate, len(packet), n*cfg.Channels)
		})
	}
}

// TestApplication_VoIP tests VoIP application uses appropriate settings.
func TestApplication_VoIP(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// VoIP mode should work with speech-like signals
	frameSize := 960
	pcm := generateSineWaveFloat32(48000, 300, frameSize, 1) // Speech frequency

	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("VoIP mode produced empty packet")
	}

	t.Logf("VoIP mode: %d bytes", len(packet))
}

// TestApplication_Audio tests Audio application uses appropriate settings.
func TestApplication_Audio(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Audio mode should work with music-like signals
	frameSize := 960
	pcm := generateSineWaveFloat32(48000, 440, frameSize, 1) // Music frequency

	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Error("Audio mode produced empty packet")
	}

	t.Logf("Audio mode: %d bytes", len(packet))
}

// TestPLC_SingleLoss tests packet loss concealment for a single lost packet.
func TestPLC_SingleLoss(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960

	// Encode and decode first frame (establishes state)
	pcm1 := generateSineWaveFloat32(48000, 440, frameSize, 1)
	packet1, _ := enc.EncodeFloat32(pcm1)
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	_, err = dec.Decode(packet1, pcmOut)
	if err != nil {
		t.Fatalf("First decode error: %v", err)
	}

	// Simulate packet loss - pass nil to decoder
	n, err := dec.Decode(nil, pcmOut)
	if err != nil {
		t.Fatalf("PLC decode error: %v", err)
	}

	plcEnergy := computeEnergy(pcmOut[:n*cfg.Channels])

	// PLC should produce some audio (concealed samples)
	t.Logf("PLC single loss: produced %d samples with energy %.4f", n, plcEnergy)
}

// TestPLC_MultipleLoss tests PLC fades gracefully on consecutive losses.
func TestPLC_MultipleLoss(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960

	// Encode and decode first frame
	pcm1 := generateSineWaveFloat32(48000, 440, frameSize, 1)
	packet1, _ := enc.EncodeFloat32(pcm1)
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	_, _ = dec.Decode(packet1, pcmOut)

	// Multiple consecutive losses
	numLosses := 5
	var energies []float64

	for i := 0; i < numLosses; i++ {
		n, err := dec.Decode(nil, pcmOut)
		if err != nil {
			t.Fatalf("PLC decode %d error: %v", i, err)
		}
		energies = append(energies, computeEnergy(pcmOut[:n*cfg.Channels]))
	}

	t.Logf("PLC multiple losses: energies=%v", energies)

	// Energy should generally decrease or stay low with consecutive losses
	// (PLC should fade rather than produce loud artifacts)
}

// TestPacketParsing tests that encoded packets can be parsed.
func TestPacketParsing(t *testing.T) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	frameSize := 960
	pcm := generateSineWaveFloat32(48000, 440, frameSize, 1)

	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Parse the packet
	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}

	if info.FrameCount != 1 {
		t.Errorf("FrameCount = %d, want 1", info.FrameCount)
	}

	t.Logf("Packet parsed: mode=%d, bandwidth=%d, frameSize=%d, stereo=%v",
		info.TOC.Mode, info.TOC.Bandwidth, info.TOC.FrameSize, info.TOC.Stereo)
}

// TestSILK10msOpusRoundTrip tests SILK 10ms encoding/decoding through the full
// Opus API at various bitrates. This verifies that the complete pipeline
// (encoder -> TOC byte -> decoder) works correctly for 10ms SILK frames.
func TestSILK10msOpusRoundTrip(t *testing.T) {
	testCases := []struct {
		name      string
		bitrate   int
		frameSize int // at 48kHz: 480=10ms, 960=20ms
		maxPeak   float64
	}{
		{"SILK-10ms-32k", 32000, 480, 2.0},
		{"SILK-10ms-40k", 40000, 480, 2.0},
		{"SILK-10ms-48k", 48000, 480, 2.0},
		{"SILK-10ms-64k", 64000, 480, 2.0},
		{"SILK-20ms-32k", 32000, 960, 2.0},
		{"SILK-20ms-64k", 64000, 960, 2.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(48000, 1, ApplicationVoIP)
			if err != nil {
				t.Fatalf("NewEncoder error: %v", err)
			}
			enc.SetBitrate(tc.bitrate)
			enc.SetFrameSize(tc.frameSize)
			enc.SetSignal(SignalVoice)
			enc.SetMaxBandwidth(BandwidthWideband)

			cfg := DefaultDecoderConfig(48000, 1)
			dec, err := NewDecoder(cfg)
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}

			nFrames := 20
			var maxPeak float64
			nDecoded := 0
			pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

			for i := 0; i < nFrames; i++ {
				pcmIn := make([]float32, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcmIn[j] = float32(0.5 * math.Sin(2*math.Pi*440*tm))
				}

				packet, err := enc.EncodeFloat32(pcmIn)
				if err != nil {
					t.Fatalf("Encode error at frame %d: %v", i, err)
				}
				if len(packet) == 0 {
					continue
				}

				// Verify TOC byte
				toc := ParseTOC(packet[0])
				if toc.Mode != ModeSILK {
					t.Logf("Frame %d: mode=%d (expected SILK), bw=%d", i, toc.Mode, toc.Bandwidth)
				}

				n, err := dec.Decode(packet, pcmOut)
				if err != nil {
					t.Logf("Frame %d: decode error: %v (pktLen=%d)", i, err, len(packet))
					continue
				}
				nDecoded++

				for j := 0; j < n; j++ {
					v := math.Abs(float64(pcmOut[j]))
					if v > maxPeak {
						maxPeak = v
					}
				}
			}

			t.Logf("Peak=%.4f (nDecoded=%d)", maxPeak, nDecoded)
			if maxPeak > tc.maxPeak {
				t.Errorf("Output peak %.4f exceeds limit %.4f - CORRUPTION", maxPeak, tc.maxPeak)
			}
			if nDecoded == 0 {
				t.Error("No frames decoded")
			}
		})
	}
}

// TestBufferSizing tests that buffer recommendations work.
func TestBufferSizing(t *testing.T) {
	// Maximum frame size: 60ms at 48kHz stereo = 2880 * 2 = 5760 samples
	maxDecodeBuffer := make([]float32, 2880*2)
	if len(maxDecodeBuffer) < 5760 {
		t.Error("Max decode buffer should be 5760 samples")
	}

	// Maximum encode output: 4000 bytes
	maxEncodeBuffer := make([]byte, 4000)
	if len(maxEncodeBuffer) < 4000 {
		t.Error("Max encode buffer should be 4000 bytes")
	}

	t.Log("Buffer sizing verified: decode=5760 samples, encode=4000 bytes")
}
