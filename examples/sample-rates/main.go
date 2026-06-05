// Package main demonstrates sample-rate handling and the int16 PCM API.
//
// Opus always codes internally at 48 kHz, but the public API accepts and
// produces PCM at any supported rate (8000, 12000, 16000, 24000, 48000 Hz):
// create the encoder and decoder at your rate and gopus resamples at the
// boundary. This is the natural fit for telephony / VoIP front ends that work
// at 8 kHz or 16 kHz.
//
// It also shows the int16 API (EncodeInt16 / DecodeInt16), which most OS and
// hardware audio paths use directly, avoiding a manual float conversion.
//
// At a given rate, a 20 ms frame is rate/50 samples per channel (160 at 8 kHz,
// 960 at 48 kHz). Decode returns samples per channel at the same rate.
//
// Usage:
//
//	go run ./examples/sample-rates
package main

import (
	"fmt"
	"log"
	"math"

	"github.com/thesyncim/gopus"
)

const channels = 1

func main() {
	fmt.Println("=== Encode/decode int16 PCM at several sample rates ===")
	for _, rate := range []int{8000, 16000, 24000, 48000} {
		frame := rate / 50 // 20 ms at this rate
		decoded, err := roundtripInt16(rate, frame)
		if err != nil {
			log.Fatalf("%d Hz: %v", rate, err)
		}
		fmt.Printf("%5d Hz: 20 ms = %4d samples/frame -> decoded %4d samples/frame\n",
			rate, frame, decoded)
	}
}

// roundtripInt16 encodes one 20 ms int16 frame at the given sample rate and
// decodes it back, returning the decoded samples per channel.
//
// The encoder and decoder are both created at the API rate; gopus handles the
// resampling to and from its internal 48 kHz pipeline.
func roundtripInt16(sampleRate, frameSize int) (int, error) {
	// VoIP suits low rates; Audio is fine too. The Application does not change
	// the I/O sample rate, only the codec's internal mode choices.
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationVoIP,
	})
	if err != nil {
		return 0, fmt.Errorf("new encoder: %w", err)
	}

	// int16 input, full-scale ~0.4 amplitude sine.
	pcm := make([]int16, frameSize*channels)
	for i := range pcm {
		pcm[i] = int16(0.4 * 32767 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
	}
	packet, err := enc.EncodeInt16Slice(pcm)
	if err != nil {
		return 0, fmt.Errorf("encode: %w", err)
	}

	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return 0, fmt.Errorf("new decoder: %w", err)
	}

	// DecodeInt16 writes int16 samples directly; size the buffer for the largest
	// frame at this rate (MaxPacketSamples is expressed per channel at the API rate).
	pcmOut := make([]int16, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.DecodeInt16(packet, pcmOut)
	if err != nil {
		return 0, fmt.Errorf("decode: %w", err)
	}
	return n, nil
}
