// Package main demonstrates the two ways gopus recovers from a lost packet on
// a real (lossy) network: packet loss concealment and in-band forward error
// correction.
//
//   - PLC (packet loss concealment): when a packet never arrives, call
//     Decode(nil, pcm). The decoder synthesizes a replacement frame from its
//     internal state, keeping the stream continuous instead of inserting a gap.
//
//   - FEC (forward error correction): when the encoder is configured with
//     in-band FEC, each packet carries a low-bitrate copy (LBRR) of the
//     PREVIOUS frame. If frame N is lost, the next packet (N+1) can reconstruct
//     it: call DecodeWithFEC(packetN+1, pcm, true) to emit the recovered frame
//     N, then Decode(packetN+1, pcm) to emit frame N+1 normally.
//
// FEC needs a speech-oriented SILK stream (set up below) and a non-zero
// expected packet-loss percentage so the encoder spends bits on LBRR.
//
// Usage:
//
//	go run ./examples/packet-loss
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
	frames := speechFrames(40)

	fmt.Println("=== Packet loss concealment: Decode(nil) ===")
	if err := demoPLC(frames); err != nil {
		log.Fatalf("PLC demo: %v", err)
	}

	fmt.Println("\n=== In-band FEC recovery: DecodeWithFEC ===")
	if err := demoFEC(frames); err != nil {
		log.Fatalf("FEC demo: %v", err)
	}
}

// demoPLC encodes a stream, drops one packet, and conceals the gap with
// Decode(nil). Concealment works for any Opus mode and needs no encoder setup.
func demoPLC(frames [][]float32) error {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		return fmt.Errorf("new encoder: %w", err)
	}

	packets := make([][]byte, len(frames))
	for i, frame := range frames {
		pkt, err := enc.EncodeFloat32(frame)
		if err != nil {
			return fmt.Errorf("encode frame %d: %w", i, err)
		}
		packets[i] = pkt
	}

	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return fmt.Errorf("new decoder: %w", err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	// Concealment fills one frame; size its buffer to the frame duration so the
	// decoder reconstructs exactly the missing 20 ms.
	concealBuf := make([]float32, frameSize*channels)

	const lostIndex = 20
	var concealedEnergy float64
	for i, pkt := range packets {
		if i == lostIndex {
			// Packet i never arrived: pass nil to run concealment.
			n, err := dec.Decode(nil, concealBuf)
			if err != nil {
				return fmt.Errorf("conceal frame %d: %w", i, err)
			}
			concealedEnergy = frameEnergy(concealBuf[:n*channels])
			continue
		}
		if _, err := dec.Decode(pkt, pcmOut); err != nil {
			return fmt.Errorf("decode frame %d: %w", i, err)
		}
	}

	fmt.Printf("concealed lost frame %d with Decode(nil); concealed RMS = %.4f\n",
		lostIndex, math.Sqrt(concealedEnergy))
	fmt.Println("the stream stayed continuous with no decode error")
	return nil
}

// demoFEC sets up a SILK voice stream with in-band FEC, drops one packet, and
// reconstructs it from the LBRR copy carried in the following packet.
func demoFEC(frames [][]float32) error {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationVoIP,
	})
	if err != nil {
		return fmt.Errorf("new encoder: %w", err)
	}
	// FEC carries the previous frame's LBRR data inside each packet. It only
	// applies to SILK/Hybrid speech frames, and the encoder only spends bits on
	// LBRR when it expects packet loss.
	if err := enc.SetMode(gopus.EncoderModeSILK); err != nil {
		return fmt.Errorf("set mode: %w", err)
	}
	if err := enc.SetBandwidth(gopus.BandwidthWideband); err != nil {
		return fmt.Errorf("set bandwidth: %w", err)
	}
	if err := enc.SetSignal(gopus.SignalVoice); err != nil {
		return fmt.Errorf("set signal: %w", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		return fmt.Errorf("set bitrate: %w", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(20); err != nil {
		return fmt.Errorf("set packet loss: %w", err)
	}

	packets := make([][]byte, len(frames))
	for i, frame := range frames {
		pkt, err := enc.EncodeFloat32(frame)
		if err != nil {
			return fmt.Errorf("encode frame %d: %w", i, err)
		}
		packets[i] = pkt
	}

	// Drop a warmed-up frame so the following packet has had time to accumulate
	// LBRR redundancy for it.
	const lostIndex = 20

	recovered, err := decodeStreamWithLoss(packets, lostIndex)
	if err != nil {
		return err
	}

	// LBRR is a low-bitrate parametric copy, so the recovered waveform is not a
	// sample-exact match for the original; the meaningful signal that FEC worked
	// is that the recovered frame carries real speech-band energy rather than the
	// decaying output of plain concealment.
	fmt.Printf("dropped frame %d, recovered it from the LBRR in packet %d\n", lostIndex, lostIndex+1)
	fmt.Printf("recovered frame RMS = %.4f (non-zero means LBRR reconstructed real audio)\n",
		math.Sqrt(frameEnergy(recovered)))
	return nil
}

// decodeStreamWithLoss decodes every packet, dropping packets[lostIndex] and
// reconstructing it from the LBRR carried in packets[lostIndex+1]. It returns
// the recovered samples for the dropped frame.
func decodeStreamWithLoss(packets [][]byte, lostIndex int) ([]float32, error) {
	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return nil, fmt.Errorf("new decoder: %w", err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	// The recovery buffer length tells DecodeWithFEC how many samples of the
	// lost frame to reconstruct, so size it to the dropped frame's duration
	// rather than to MaxPacketSamples.
	recoverBuf := make([]float32, frameSize*channels)

	var recovered []float32
	for i := 0; i < len(packets); i++ {
		if i == lostIndex {
			// Frame lostIndex was dropped. Recover it from the next packet's
			// LBRR data before decoding that next packet normally.
			n, err := dec.DecodeWithFEC(packets[i+1], recoverBuf, true)
			if err != nil {
				return nil, fmt.Errorf("FEC-recover frame %d: %w", i, err)
			}
			recovered = append([]float32(nil), recoverBuf[:n*channels]...)

			if _, err := dec.Decode(packets[i+1], pcmOut); err != nil {
				return nil, fmt.Errorf("decode frame %d: %w", i+1, err)
			}
			i++ // packet i+1 is now consumed
			continue
		}
		if _, err := dec.Decode(packets[i], pcmOut); err != nil {
			return nil, fmt.Errorf("decode frame %d: %w", i, err)
		}
	}
	return recovered, nil
}

// speechFrames builds a deterministic voiced + harmonic test signal that the
// SILK encoder treats as speech.
func speechFrames(n int) [][]float32 {
	frames := make([][]float32, n)
	for f := range n {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			t := float64(f*frameSize+i) / sampleRate
			pcm[i] = 0.38*float32(math.Sin(2*math.Pi*220*t)) +
				0.14*float32(math.Sin(2*math.Pi*440*t+0.11))
		}
		frames[f] = pcm
	}
	return frames
}

func frameEnergy(pcm []float32) float64 {
	if len(pcm) == 0 {
		return 0
	}
	var sum float64
	for _, s := range pcm {
		sum += float64(s) * float64(s)
	}
	return sum / float64(len(pcm))
}
