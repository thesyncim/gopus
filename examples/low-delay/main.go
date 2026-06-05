// Package main demonstrates low-delay, CELT-only encoding.
//
// ApplicationLowDelay (the OPUS_APPLICATION_RESTRICTED_LOWDELAY mode) forces the
// CELT layer and skips the SILK lookahead, minimizing the codec's algorithmic
// delay. Combined with short frames (2.5 / 5 / 10 ms via SetFrameSize) this is
// the configuration for interactive audio: live monitoring, music
// collaboration, or game voice.
//
// The example encodes a tone at several frame sizes and reports the encoder's
// algorithmic delay (Lookahead) for each, contrasting low-delay CELT with the
// default audio profile. The shorter the frame and the lower the lookahead, the
// less mouth-to-ear latency.
//
// Usage:
//
//	go run ./examples/low-delay
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
)

func main() {
	fmt.Println("=== Low-delay CELT vs default audio profile ===")

	// The default audio profile may use SILK/Hybrid and a longer lookahead.
	def, err := newEncoder(gopus.ApplicationAudio, 960)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("ApplicationAudio   (20  ms frames): lookahead = %s\n", delayString(def.Lookahead()))

	// Low-delay forces CELT and trims the lookahead. CELT also unlocks the
	// shortest Opus frame sizes (down to 2.5 ms = 120 samples at 48 kHz).
	for _, frame := range []int{480, 240, 120} { // 10 ms, 5 ms, 2.5 ms
		lookahead, mode, err := roundtripLowDelay(frame)
		if err != nil {
			log.Fatalf("%s: %v", frameLabel(frame), err)
		}
		fmt.Printf("ApplicationLowDelay (%-5s frames): lookahead = %s, decoded mode = %s\n",
			frameLabel(frame), delayString(lookahead), modeName(mode))
	}
}

// roundtripLowDelay configures a CELT-only low-delay encoder for the given frame
// size, encodes one matching frame, decodes it, and returns the encoder
// lookahead and the coding mode the decoder observed (expected to be CELT).
func roundtripLowDelay(frameSize int) (lookahead int, mode gopus.Mode, err error) {
	enc, err := newEncoder(gopus.ApplicationLowDelay, frameSize)
	if err != nil {
		return 0, 0, err
	}

	// The input PCM length must match the configured frame size.
	pcm := make([]float32, frameSize*channels)
	for i := range pcm {
		pcm[i] = float32(0.4 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		return 0, 0, fmt.Errorf("encode: %w", err)
	}

	// The TOC byte of any packet reveals its coding mode.
	mode = gopus.ParseTOC(packet[0]).Mode

	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return 0, 0, fmt.Errorf("new decoder: %w", err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	if _, err := dec.Decode(packet, pcmOut); err != nil {
		return 0, 0, fmt.Errorf("decode: %w", err)
	}

	return enc.Lookahead(), mode, nil
}

func newEncoder(app gopus.Application, frameSize int) (*gopus.Encoder, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: app,
	})
	if err != nil {
		return nil, fmt.Errorf("new encoder: %w", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		return nil, fmt.Errorf("set frame size: %w", err)
	}
	return enc, nil
}

// delayString renders a per-channel sample count as samples and milliseconds.
func delayString(samples int) string {
	return fmt.Sprintf("%d samples (%.2f ms)", samples, float64(samples)*1000/sampleRate)
}

// frameLabel renders a 48 kHz frame size in milliseconds.
func frameLabel(frameSize int) string {
	return fmt.Sprintf("%.1f ms", float64(frameSize)*1000/sampleRate)
}

func modeName(m gopus.Mode) string {
	switch m {
	case gopus.ModeSILK:
		return "SILK"
	case gopus.ModeHybrid:
		return "Hybrid"
	case gopus.ModeCELT:
		return "CELT"
	default:
		return "unknown"
	}
}
