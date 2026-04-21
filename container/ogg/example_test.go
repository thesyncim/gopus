package ogg_test

import (
	"bytes"
	"fmt"
	"log"
	"math"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

func ExampleNewWriter() {
	// Create a buffer to write Ogg Opus data
	var buf bytes.Buffer

	// Create writer for 48kHz stereo
	w, err := ogg.NewWriter(&buf, uint32(48000), uint8(2))
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()

	fmt.Println("Ogg Opus writer created")
	// Output: Ogg Opus writer created
}

func ExampleWriter_WritePacket() {
	var buf bytes.Buffer
	w, err := ogg.NewWriter(&buf, uint32(48000), uint8(2))
	if err != nil {
		log.Fatal(err)
	}

	// Create an encoder
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}

	// Encode and write a frame
	pcm := make([]float32, 960*2) // 20ms stereo
	packetBuf := make([]byte, 4000)
	nPacket, err := enc.Encode(pcm, packetBuf)
	if err != nil {
		log.Fatal(err)
	}

	// Write packet with sample count
	err = w.WritePacket(packetBuf[:nPacket], 960)
	if err != nil {
		log.Fatal(err)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}

	r, err := ogg.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		log.Fatal(err)
	}
	packet, granule, err := r.ReadPacket()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Stored packet at granule %d: %t\n", granule, len(packet) > 0)
	// Output: Stored packet at granule 960: true
}

func ExampleNewReader() {
	// First create some Ogg Opus data
	var buf bytes.Buffer
	w, err := ogg.NewWriter(&buf, uint32(48000), uint8(2))
	if err != nil {
		log.Fatal(err)
	}

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
	if err := w.WritePacket(packetBuf[:nPacket], 960); err != nil {
		log.Fatal(err)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}

	// Now read it back
	r, err := ogg.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Ogg Opus: %d channels, %d Hz\n", r.Channels(), r.SampleRate())
	// Output: Ogg Opus: 2 channels, 48000 Hz
}

func ExampleReader_ReadPacket() {
	// Create Ogg data with multiple packets
	var buf bytes.Buffer
	w, err := ogg.NewWriter(&buf, uint32(48000), uint8(1))
	if err != nil {
		log.Fatal(err)
	}
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		pcm := make([]float32, 960)
		for j := range pcm {
			pcm[j] = float32(math.Sin(float64(i*960+j) * 0.01))
		}
		packetBuf := make([]byte, 4000)
		nPacket, err := enc.Encode(pcm, packetBuf)
		if err != nil {
			log.Fatal(err)
		}
		if err := w.WritePacket(packetBuf[:nPacket], 960); err != nil {
			log.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}

	// Read packets back
	r, err := ogg.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		log.Fatal(err)
	}
	cfg := gopus.DefaultDecoderConfig(48000, 1)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		log.Fatal(err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	packetCount := 0
	for {
		packet, _, err := r.ReadPacket()
		if err != nil {
			break
		}
		_, _ = dec.Decode(packet, pcmOut)
		packetCount++
	}

	fmt.Printf("Decoded %d packets\n", packetCount)
	// Output: Decoded 5 packets
}

func Example_writeOggFile() {
	// Demonstrate creating an Ogg Opus file
	var buf bytes.Buffer

	// Create writer
	w, err := ogg.NewWriter(&buf, uint32(48000), uint8(2))
	if err != nil {
		log.Fatal(err)
	}

	// Create encoder
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		log.Fatal(err)
	}

	// Write 1 second of audio (50 frames of 20ms each)
	for i := 0; i < 50; i++ {
		pcm := make([]float32, 960*2)
		// Generate stereo sine wave
		for j := 0; j < 960; j++ {
			t := float64(i*960+j) / 48000.0
			pcm[j*2] = float32(math.Sin(2 * math.Pi * 440 * t))   // Left: 440 Hz
			pcm[j*2+1] = float32(math.Sin(2 * math.Pi * 554 * t)) // Right: 554 Hz
		}
		packetBuf := make([]byte, 4000)
		nPacket, err := enc.Encode(pcm, packetBuf)
		if err != nil {
			log.Fatal(err)
		}
		if err := w.WritePacket(packetBuf[:nPacket], 960); err != nil {
			log.Fatal(err)
		}
	}

	if err := w.Close(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Created 1-second Ogg Opus stream with %d frames\n", 50)
	// Output: Created 1-second Ogg Opus stream with 50 frames
}

func ExampleWriter_Close() {
	var buf bytes.Buffer
	w, err := ogg.NewWriter(&buf, uint32(48000), uint8(1))
	if err != nil {
		log.Fatal(err)
	}

	// Write some audio
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}
	pcm := make([]float32, 960)
	packetBuf := make([]byte, 4000)
	nPacket, err := enc.Encode(pcm, packetBuf)
	if err != nil {
		log.Fatal(err)
	}
	if err := w.WritePacket(packetBuf[:nPacket], 960); err != nil {
		log.Fatal(err)
	}

	// Close writes the EOS page
	err = w.Close()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Stream closed with EOS page")
	// Output: Stream closed with EOS page
}

func Example_roundTripOgg() {
	// Complete Ogg Opus round-trip: encode to file, decode from file
	var buf bytes.Buffer

	// Encode to Ogg
	w, err := ogg.NewWriter(&buf, uint32(48000), uint8(2))
	if err != nil {
		log.Fatal(err)
	}
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}

	// Encode 10 frames
	for i := 0; i < 10; i++ {
		pcm := make([]float32, 960*2)
		for j := range pcm {
			pcm[j] = float32(math.Sin(float64(i*960*2+j) * 0.005))
		}
		packetBuf := make([]byte, 4000)
		nPacket, err := enc.Encode(pcm, packetBuf)
		if err != nil {
			log.Fatal(err)
		}
		if err := w.WritePacket(packetBuf[:nPacket], 960); err != nil {
			log.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}

	// Decode from Ogg
	r, err := ogg.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		log.Fatal(err)
	}
	cfg := gopus.DefaultDecoderConfig(48000, 2)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		log.Fatal(err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	totalSamples := 0
	for {
		packet, _, err := r.ReadPacket()
		if err != nil {
			break
		}
		n, _ := dec.Decode(packet, pcmOut)
		totalSamples += n
	}

	fmt.Printf("Round-trip: wrote 10 frames, decoded %d samples\n", totalSamples)
	// Output: Round-trip: wrote 10 frames, decoded 9600 samples
}
