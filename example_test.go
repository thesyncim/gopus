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

	if err := enc.SetBitrate(64000); err != nil { // bits per second
		log.Fatal(err)
	}
	if err := enc.SetComplexity(10); err != nil {
		log.Fatal(err)
	}

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
	packetBuf := make([]byte, 1500)

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
	packetBuf := make([]byte, 1500)
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
	packetBuf := make([]byte, 1500)
	nPacket, err := enc.Encode(pcm, packetBuf)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := dec.Decode(packetBuf[:nPacket], pcmOut); err != nil {
		log.Fatal(err)
	}

	// Simulate packet loss by passing nil. Request one frame of PLC output
	// (buffer sized to the last packet duration, matching libopus frame_size).
	plcBuf := make([]float32, 960*cfg.Channels)
	n, err := dec.Decode(nil, plcBuf)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("PLC generated %d samples per channel\n", n)
	// Output: PLC generated 960 samples per channel
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
	packetBuf := make([]byte, 1500)
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

func ExampleDecoder_DecodeWithFEC() {
	const frameSize = 960 // 20 ms at 48 kHz, per channel
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  48000,
		Channels:    1,
		Application: gopus.ApplicationVoIP,
	})
	if err != nil {
		log.Fatal(err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(20); err != nil {
		log.Fatal(err)
	}
	cfg := gopus.DefaultDecoderConfig(48000, 1)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		log.Fatal(err)
	}
	input := make([]float32, frameSize)
	packet := make([]byte, cfg.MaxPacketBytes)
	output := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	// Deliver the first packet, lose the second, and receive the third.
	for frame := range 3 {
		for i := range input {
			input[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(frame*frameSize+i)/48000))
		}
		n, err := enc.Encode(input, packet)
		if err != nil {
			log.Fatal(err)
		}
		if frame == 0 {
			if _, err := dec.Decode(packet[:n], output); err != nil {
				log.Fatal(err)
			}
		}
		if frame != 2 {
			continue
		}

		// Request the missing duration first. The decoder uses PLC if the
		// following packet does not carry FEC for this loss.
		recovered, err := dec.DecodeWithFEC(packet[:n], output[:frameSize], true)
		if err != nil {
			log.Fatal(err)
		}
		// Consume the recovered output before reusing its storage below.
		fmt.Printf("recovered %d samples\n", recovered)
		decoded, err := dec.Decode(packet[:n], output)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("decoded %d samples\n", decoded)
	}
	// Output:
	// recovered 960 samples
	// decoded 960 samples
}

func ExampleSpeechDetector_AnalyzeInt16() {
	det, err := gopus.NewSpeechDetector(16000)
	if err != nil {
		log.Fatal(err)
	}

	// One 20 ms frame of 16 kHz mono PCM. Real audio replaces the zeros.
	frame := make([]int16, 320)
	probability, err := det.AnalyzeInt16(frame)
	if err != nil {
		log.Fatal(err)
	}

	// Compare against a threshold of your choosing; exact digital silence scores 0.
	fmt.Printf("speech probability %.2f\n", probability)
	// Output: speech probability 0.00
}
