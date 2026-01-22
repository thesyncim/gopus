package ogg_test

import (
	"bytes"
	"fmt"
	"log"
	"math"

	"gopus"
	"gopus/container/ogg"
)

func ExampleNewWriter() {
	// Create a buffer to write Ogg Opus data
	var buf bytes.Buffer

	// Create writer for 48kHz stereo
	w, err := ogg.NewWriter(&buf, 48000, 2)
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()

	fmt.Println("Ogg Opus writer created")
	// Output: Ogg Opus writer created
}

func ExampleWriter_WritePacket() {
	var buf bytes.Buffer
	w, _ := ogg.NewWriter(&buf, 48000, 2)
	defer w.Close()

	// Create an encoder
	enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)

	// Encode and write a frame
	pcm := make([]float32, 960*2) // 20ms stereo
	packet, _ := enc.EncodeFloat32(pcm)

	// Write packet with sample count
	err := w.WritePacket(packet, 960)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Wrote %d bytes to Ogg container\n", len(packet))
}

func ExampleNewReader() {
	// First create some Ogg Opus data
	var buf bytes.Buffer
	w, _ := ogg.NewWriter(&buf, 48000, 2)

	enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
	pcm := make([]float32, 960*2)
	packet, _ := enc.EncodeFloat32(pcm)
	w.WritePacket(packet, 960)
	w.Close()

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
	w, _ := ogg.NewWriter(&buf, 48000, 1)
	enc, _ := gopus.NewEncoder(48000, 1, gopus.ApplicationAudio)

	for i := 0; i < 5; i++ {
		pcm := make([]float32, 960)
		for j := range pcm {
			pcm[j] = float32(math.Sin(float64(i*960+j) * 0.01))
		}
		packet, _ := enc.EncodeFloat32(pcm)
		w.WritePacket(packet, 960)
	}
	w.Close()

	// Read packets back
	r, _ := ogg.NewReader(bytes.NewReader(buf.Bytes()))
	dec, _ := gopus.NewDecoder(48000, 1)

	packetCount := 0
	for {
		packet, _, err := r.ReadPacket()
		if err != nil {
			break
		}
		_, _ = dec.DecodeFloat32(packet)
		packetCount++
	}

	fmt.Printf("Decoded %d packets\n", packetCount)
	// Output: Decoded 5 packets
}

func Example_writeOggFile() {
	// Demonstrate creating an Ogg Opus file
	var buf bytes.Buffer

	// Create writer
	w, _ := ogg.NewWriter(&buf, 48000, 2)

	// Create encoder
	enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
	enc.SetBitrate(128000)

	// Write 1 second of audio (50 frames of 20ms each)
	for i := 0; i < 50; i++ {
		pcm := make([]float32, 960*2)
		// Generate stereo sine wave
		for j := 0; j < 960; j++ {
			t := float64(i*960+j) / 48000.0
			pcm[j*2] = float32(math.Sin(2 * math.Pi * 440 * t))   // Left: 440 Hz
			pcm[j*2+1] = float32(math.Sin(2 * math.Pi * 554 * t)) // Right: 554 Hz
		}
		packet, _ := enc.EncodeFloat32(pcm)
		w.WritePacket(packet, 960)
	}

	w.Close()

	fmt.Printf("Created Ogg Opus: %d bytes, 1 second of stereo audio\n", buf.Len())
}

func ExampleWriter_Close() {
	var buf bytes.Buffer
	w, _ := ogg.NewWriter(&buf, 48000, 1)

	// Write some audio
	enc, _ := gopus.NewEncoder(48000, 1, gopus.ApplicationAudio)
	pcm := make([]float32, 960)
	packet, _ := enc.EncodeFloat32(pcm)
	w.WritePacket(packet, 960)

	// Close writes the EOS page
	err := w.Close()
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
	w, _ := ogg.NewWriter(&buf, 48000, 2)
	enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)

	// Encode 10 frames
	for i := 0; i < 10; i++ {
		pcm := make([]float32, 960*2)
		for j := range pcm {
			pcm[j] = float32(math.Sin(float64(i*960*2+j) * 0.005))
		}
		packet, _ := enc.EncodeFloat32(pcm)
		w.WritePacket(packet, 960)
	}
	w.Close()

	// Decode from Ogg
	r, _ := ogg.NewReader(bytes.NewReader(buf.Bytes()))
	dec, _ := gopus.NewDecoder(48000, 2)

	totalSamples := 0
	for {
		packet, _, err := r.ReadPacket()
		if err != nil {
			break
		}
		pcm, _ := dec.DecodeFloat32(packet)
		totalSamples += len(pcm) / 2
	}

	fmt.Printf("Round-trip: wrote 10 frames, decoded %d samples\n", totalSamples)
}
