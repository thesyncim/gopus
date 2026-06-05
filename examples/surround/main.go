// Package main demonstrates multistream (surround) encoding and decoding.
//
// Opus carries more than two channels by splitting the input into several
// elementary Opus streams (some coupled into stereo pairs, some mono) and
// describing the layout with a channel-mapping table. gopus exposes this through
// MultistreamEncoder / MultistreamDecoder.
//
// For the standard Vorbis-style channel orders used by Ogg Opus (1-8 channels:
// mono, stereo, 3.0, quad, 5.0, 5.1, 6.1, 7.1) the *Default constructors pick
// the stream count, coupling, and mapping for you. This example encodes and
// decodes 5.1 surround that way, then prints the resulting stream layout.
//
// Use the explicit NewMultistreamEncoder / NewMultistreamDecoder constructors
// when you need a custom mapping (for example >8 channels or a non-standard
// speaker order); see the package docs on those constructors for the mapping
// table format.
//
// Usage:
//
//	go run ./examples/surround
package main

import (
	"fmt"
	"log"
	"math"

	"github.com/thesyncim/gopus"
)

const (
	sampleRate = 48000
	channels   = 6   // 5.1 surround: FL, C, FR, RL, RR, LFE (Vorbis order)
	frameSize  = 960 // 20 ms at 48 kHz
)

func main() {
	decoded, layout, err := roundtripSurround(channels, frameCount(2))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("encoded/decoded %d-channel surround (%s)\n", channels, layout)
	fmt.Printf("recovered %d interleaved samples (%d per channel)\n",
		len(decoded), len(decoded)/channels)
}

// streamLayout is a short human-readable description of how the channels map
// onto elementary Opus streams.
func streamLayout(streams, coupled int) string {
	return fmt.Sprintf("%d streams, %d coupled (stereo) + %d mono",
		streams, coupled, streams-coupled)
}

// roundtripSurround encodes a multi-frame surround signal with the default
// mapping and decodes it back, returning the decoded interleaved PCM and the
// stream layout the encoder chose.
func roundtripSurround(channels, frames int) (decoded []float32, layout string, err error) {
	// The default constructors derive the stream count, coupling, and Vorbis
	// channel mapping from the channel count alone.
	enc, err := gopus.NewMultistreamEncoderDefault(sampleRate, channels, gopus.ApplicationAudio)
	if err != nil {
		return nil, "", fmt.Errorf("new multistream encoder: %w", err)
	}
	// Surround bitrate is shared across all streams; the encoder splits it.
	if err := enc.SetBitrate(256000); err != nil {
		return nil, "", fmt.Errorf("set bitrate: %w", err)
	}

	dec, err := gopus.NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		return nil, "", fmt.Errorf("new multistream decoder: %w", err)
	}
	layout = streamLayout(enc.Streams(), enc.CoupledStreams())

	// Multistream PCM is interleaved across all channels, same as stereo just
	// wider: [c0, c1, ..., c5, c0, c1, ...]. Size the decode buffer to the
	// largest frame the decoder may emit, times the channel count.
	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	pcmOut := make([]float32, cfg.MaxPacketSamples*channels)

	for f := range frames {
		pcm := surroundFrame(f)

		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			return nil, "", fmt.Errorf("encode frame %d: %w", f, err)
		}

		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			return nil, "", fmt.Errorf("decode frame %d: %w", f, err)
		}
		decoded = append(decoded, pcmOut[:n*channels]...)
	}

	return decoded, layout, nil
}

// surroundFrame builds one interleaved 5.1 frame, giving each channel a
// distinct tone so the per-channel routing is audible/measurable.
func surroundFrame(frame int) []float32 {
	// Distinct frequency per surround channel (FL, C, FR, RL, RR, LFE).
	freqs := [channels]float64{220, 330, 440, 550, 660, 60}
	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		t := float64(frame*frameSize+i) / sampleRate
		for ch := range channels {
			pcm[i*channels+ch] = float32(0.25 * math.Sin(2*math.Pi*freqs[ch]*t))
		}
	}
	return pcm
}

func frameCount(seconds float64) int {
	return int(seconds*sampleRate) / frameSize
}
