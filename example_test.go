package gopus_test

import (
	"fmt"
	"log"
	"math"

	"github.com/thesyncim/gopus"
)

func ExampleNewEncoder() {
	// Create an encoder for 48kHz stereo audio
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
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
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Decoder: %dHz, %d channels\n", dec.SampleRate(), dec.Channels())
	// Output: Decoder: 48000Hz, 2 channels
}

func ExampleEncoder_Encode() {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}

	// Generate 20ms of stereo silence (960 samples per channel)
	pcm := make([]float32, 960*2)
	packetBuf := make([]byte, 4000)

	// Encode the frame
	n, err := enc.Encode(pcm, packetBuf)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Encoded %d PCM samples into packet: %t\n", len(pcm)/enc.Channels(), n > 0)
	// Output: Encoded 960 PCM samples into packet: true
}

func ExampleDecoder_Decode() {
	// Create encoder and decoder
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}
	cfg := gopus.DefaultDecoderConfig(48000, 2)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		log.Fatal(err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	// Encode one 20ms stereo frame.
	pcm := make([]float32, 960*2)
	packetBuf := make([]byte, 4000)
	nPacket, err := enc.Encode(pcm, packetBuf)
	if err != nil {
		log.Fatal(err)
	}

	// Decode the packet
	n, err := dec.Decode(packetBuf[:nPacket], pcmOut)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Decoded %d samples per channel\n", n)
	// Output: Decoded 960 samples per channel
}

func ExampleDecoder_Decode_packetLoss() {
	cfg := gopus.DefaultDecoderConfig(48000, 2)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		log.Fatal(err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	// First, decode a real packet to initialize state
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}
	pcm := make([]float32, 960*2)
	packetBuf := make([]byte, 4000)
	nPacket, err := enc.Encode(pcm, packetBuf)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := dec.Decode(packetBuf[:nPacket], pcmOut); err != nil {
		log.Fatal(err)
	}

	// Simulate packet loss by passing nil
	// Decoder uses PLC to generate concealment audio
	n, err := dec.Decode(nil, pcmOut)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("PLC generated %d samples per channel\n", n)
	// Output: PLC generated 960 samples per channel
}

func ExampleSupportsOptionalExtension() {
	fmt.Printf("dnn_blob: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionDNNBlob))
	fmt.Printf("dred: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionDRED))
	fmt.Printf("osce_bwe: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionOSCEBWE))
	fmt.Printf("qext: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionQEXT))
	// Output:
	// dnn_blob: true
	// dred: false
	// osce_bwe: false
	// qext: true
}

func Example_roundTrip() {
	// Complete encode-decode round trip
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		log.Fatal(err)
	}
	cfg := gopus.DefaultDecoderConfig(48000, 1)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		log.Fatal(err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	// 20ms of mono audio at 48kHz
	input := make([]float32, 960)
	for i := range input {
		input[i] = float32(math.Sin(float64(i) * 0.02))
	}

	// Encode
	packetBuf := make([]byte, 4000)
	nPacket, err := enc.Encode(input, packetBuf)
	if err != nil {
		log.Fatal(err)
	}

	// Decode
	n, err := dec.Decode(packetBuf[:nPacket], pcmOut)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Round trip ok: %t\n", nPacket > 0 && n == len(input))
	// Output: Round trip ok: true
}

func ExampleEncoder_SetBitrate() {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}

	// Set bitrate to 128 kbps
	err = enc.SetBitrate(128000)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Bitrate set to %d bps\n", enc.Bitrate())
	// Output: Bitrate set to 128000 bps
}

func ExampleEncoder_SetComplexity() {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}

	// Set complexity to maximum quality
	err = enc.SetComplexity(10)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Complexity: %d\n", enc.Complexity())
	// Output: Complexity: 10
}

func ExampleEncoder_SetDTX() {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		log.Fatal(err)
	}

	// Enable DTX for bandwidth savings during silence
	enc.SetDTX(true)

	fmt.Printf("DTX enabled: %v\n", enc.DTXEnabled())
	// Output: DTX enabled: true
}

func ExampleEncoder_SetFEC() {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationVoIP})
	if err != nil {
		log.Fatal(err)
	}

	// Enable FEC for packet loss recovery
	enc.SetFEC(true)

	fmt.Printf("FEC enabled: %v\n", enc.FECEnabled())
	// Output: FEC enabled: true
}
