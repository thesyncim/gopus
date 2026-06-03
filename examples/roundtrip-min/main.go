// Package main is the smallest useful gopus program: it encodes one frame of
// PCM to an Opus packet and decodes it straight back, using the caller-owned
// buffer Encode/Decode API.
//
// This is the recommended starting point for the public API. For quality
// metrics across signal types and bitrates see ../roundtrip; for file I/O see
// ../ogg-file; for loss recovery see ../packet-loss.
//
// Usage:
//
//	go run ./examples/roundtrip-min
package main

import (
	"fmt"
	"log"
	"math"

	"github.com/thesyncim/gopus"
)

const (
	sampleRate = 48000
	channels   = 1
	frameSize  = 960 // 20 ms at 48 kHz
)

func main() {
	// One 20 ms frame of a 440 Hz sine wave at half amplitude.
	pcm := make([]float32, frameSize*channels)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
	}

	packet, n, err := encodeDecode(pcm)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("encoded %d input samples into a %d-byte Opus packet\n", len(pcm), len(packet))
	fmt.Printf("decoded back into %d samples per channel\n", n)
}

// encodeDecode encodes one frame and decodes it again, returning the packet and
// the number of samples per channel produced by the decoder.
func encodeDecode(pcm []float32) (packet []byte, decodedSamples int, err error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("new encoder: %w", err)
	}

	// 4000 bytes is large enough for any single Opus packet. Encode writes into
	// this caller-owned buffer and returns how many bytes it used.
	packetBuf := make([]byte, 4000)
	nPacket, err := enc.Encode(pcm, packetBuf)
	if err != nil {
		return nil, 0, fmt.Errorf("encode: %w", err)
	}
	packet = packetBuf[:nPacket]

	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("new decoder: %w", err)
	}

	// The output buffer must have room for the largest packet the decoder may
	// emit: MaxPacketSamples per channel. Decode returns samples per channel.
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		return nil, 0, fmt.Errorf("decode: %w", err)
	}

	return packet, n, nil
}
