package gopus_test

import (
	"fmt"
	"log"
	"math"

	"github.com/thesyncim/gopus"
)

func ExampleNewEncoder() {
	// Create an encoder for 48kHz stereo audio
	enc, err := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
	if err != nil {
		log.Fatal(err)
	}

	// Configure encoder settings
	enc.SetBitrate(64000) // 64 kbps
	enc.SetComplexity(10) // Maximum quality

	fmt.Printf("Encoder: %dHz, %d channels\n", enc.SampleRate(), enc.Channels())
	// Output: Encoder: 48000Hz, 2 channels
}

func ExampleNewDecoder() {
	// Create a decoder for 48kHz stereo audio
	dec, err := gopus.NewDecoderDefault(48000, 2)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Decoder: %dHz, %d channels\n", dec.SampleRate(), dec.Channels())
	// Output: Decoder: 48000Hz, 2 channels
}

func ExampleEncoder_EncodeFloat32() {
	enc, err := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
	if err != nil {
		log.Fatal(err)
	}

	// Generate 20ms of stereo silence (960 samples per channel)
	pcm := make([]float32, 960*2)

	// Encode the frame
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Encoded %d samples to %d bytes\n", len(pcm)/2, len(packet))
	// Output will vary based on encoder state
}

func ExampleDecoder_Decode() {
	// Create encoder and decoder
	enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
	cfg := gopus.DefaultDecoderConfig(48000, 2)
	dec, _ := gopus.NewDecoder(cfg)
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	// Generate and encode a test signal
	pcm := make([]float32, 960*2)
	for i := range pcm {
		pcm[i] = float32(math.Sin(float64(i) * 0.01))
	}

	packet, _ := enc.EncodeFloat32(pcm)

	// Decode the packet
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Decoded %d bytes to %d samples\n", len(packet), n)
}

func ExampleDecoder_Decode_packetLoss() {
	cfg := gopus.DefaultDecoderConfig(48000, 2)
	dec, _ := gopus.NewDecoder(cfg)
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	// First, decode a real packet to initialize state
	enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
	pcm := make([]float32, 960*2)
	packet, _ := enc.EncodeFloat32(pcm)
	_, _ = dec.Decode(packet, pcmOut)

	// Simulate packet loss by passing nil
	// Decoder uses PLC to generate concealment audio
	n, err := dec.Decode(nil, pcmOut)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("PLC generated %d samples\n", n)
}

func Example_roundTrip() {
	// Complete encode-decode round trip
	enc, _ := gopus.NewEncoder(48000, 1, gopus.ApplicationVoIP)
	cfg := gopus.DefaultDecoderConfig(48000, 1)
	dec, _ := gopus.NewDecoder(cfg)
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	// 20ms of mono audio at 48kHz
	input := make([]float32, 960)
	for i := range input {
		input[i] = float32(math.Sin(float64(i) * 0.02))
	}

	// Encode
	packet, _ := enc.EncodeFloat32(input)

	// Decode
	n, _ := dec.Decode(packet, pcmOut)

	fmt.Printf("Round trip: %d samples -> %d bytes -> %d samples\n",
		len(input), len(packet), n)
}

func ExampleEncoder_SetBitrate() {
	enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)

	// Set bitrate to 128 kbps
	err := enc.SetBitrate(128000)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Bitrate set to %d bps\n", enc.Bitrate())
	// Output: Bitrate set to 128000 bps
}

func ExampleEncoder_SetComplexity() {
	enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)

	// Set complexity to maximum quality
	err := enc.SetComplexity(10)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Complexity: %d\n", enc.Complexity())
	// Output: Complexity: 10
}

func ExampleEncoder_SetDTX() {
	enc, _ := gopus.NewEncoder(48000, 1, gopus.ApplicationVoIP)

	// Enable DTX for bandwidth savings during silence
	enc.SetDTX(true)

	fmt.Printf("DTX enabled: %v\n", enc.DTXEnabled())
	// Output: DTX enabled: true
}

func ExampleEncoder_SetFEC() {
	enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationVoIP)

	// Enable FEC for packet loss recovery
	enc.SetFEC(true)

	fmt.Printf("FEC enabled: %v\n", enc.FECEnabled())
	// Output: FEC enabled: true
}
