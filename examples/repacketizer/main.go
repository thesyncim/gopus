// Package main demonstrates the Opus repacketizer and packet-inspection helpers.
//
// A single Opus packet can carry several 20 ms frames (a "code 3" packet). The
// Repacketizer regroups frames without re-encoding: feed it whole packets with
// Cat, then emit any contiguous frame range with Out / OutRange. This is how you
// merge many small network packets into one container packet, or split one large
// packet back into smaller transport units, at zero quality cost.
//
// This example also shows the read-only ParsePacket helper (frame count and
// per-frame byte sizes) and the in-place PacketPad / PacketUnpad helpers used to
// hit a fixed CBR packet size.
//
// Usage:
//
//	go run ./examples/repacketizer
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
	// Encode four separate 20 ms packets, as if each had arrived on the network.
	packets, err := encodeFrames(4)
	if err != nil {
		log.Fatal(err)
	}
	for i, p := range packets {
		info, err := gopus.ParsePacket(p)
		if err != nil {
			log.Fatalf("parse packet %d: %v", i, err)
		}
		fmt.Printf("input packet %d: %d byte(s), %d frame(s)\n", i, info.TotalSize, info.FrameCount)
	}

	// Merge all four into one multi-frame packet without re-encoding.
	merged, err := merge(packets)
	if err != nil {
		log.Fatal(err)
	}
	mergedInfo, err := gopus.ParsePacket(merged)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nmerged packet: %d byte(s), %d frame(s) (%d ms of audio)\n",
		mergedInfo.TotalSize, mergedInfo.FrameCount, mergedInfo.FrameCount*frameSize*1000/sampleRate)

	// Split the merged packet back into two 2-frame halves with OutRange.
	first, second, err := splitInHalf(merged)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("split back into 2 packets of %d and %d frame(s)\n",
		frameCount(first), frameCount(second))

	// Pad the first half up to a fixed transport size, then strip the padding.
	padded, err := padTo(first, len(first)+16)
	if err != nil {
		log.Fatal(err)
	}
	unpaddedLen, err := gopus.PacketUnpad(padded, len(padded))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\npadded %d -> %d byte(s), then unpadded back to %d byte(s)\n",
		len(first), len(padded), unpaddedLen)

	// The merged packet decodes to the full concatenated audio in one call.
	samples, err := decodeAll(merged)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("merged packet decoded to %d samples (%d frames x %d)\n",
		samples, mergedInfo.FrameCount, frameSize)
}

// merge concatenates whole packets into a single multi-frame packet using the
// repacketizer. All inputs must share the same TOC configuration (here they do,
// because one encoder with fixed settings produced them).
func merge(packets [][]byte) ([]byte, error) {
	rp := gopus.NewRepacketizer()
	for i, p := range packets {
		if err := rp.Cat(p); err != nil {
			return nil, fmt.Errorf("cat packet %d: %w", i, err)
		}
	}

	// 4000 bytes is large enough for any single Opus packet.
	out := make([]byte, 4000)
	n, err := rp.Out(out)
	if err != nil {
		return nil, fmt.Errorf("repacketizer out: %w", err)
	}
	return out[:n], nil
}

// splitInHalf re-feeds a multi-frame packet and emits two packets, each holding
// half of its frames, via OutRange(begin, end).
func splitInHalf(packet []byte) (first, second []byte, err error) {
	rp := gopus.NewRepacketizer()
	if err := rp.Cat(packet); err != nil {
		return nil, nil, fmt.Errorf("cat: %w", err)
	}
	mid := rp.NumFrames() / 2

	firstBuf := make([]byte, 4000)
	n, err := rp.OutRange(0, mid, firstBuf)
	if err != nil {
		return nil, nil, fmt.Errorf("out range [0,%d): %w", mid, err)
	}
	first = firstBuf[:n]

	secondBuf := make([]byte, 4000)
	n, err = rp.OutRange(mid, rp.NumFrames(), secondBuf)
	if err != nil {
		return nil, nil, fmt.Errorf("out range [%d,%d): %w", mid, rp.NumFrames(), err)
	}
	second = secondBuf[:n]
	return first, second, nil
}

// padTo grows a packet in place to exactly newLen bytes. PacketPad needs the
// backing array to have capacity for newLen, so the buffer is allocated up front.
func padTo(packet []byte, newLen int) ([]byte, error) {
	buf := make([]byte, len(packet), newLen)
	copy(buf, packet)
	if err := gopus.PacketPad(buf, len(packet), newLen); err != nil {
		return nil, fmt.Errorf("pad: %w", err)
	}
	return buf[:newLen], nil
}

// frameCount reports how many frames a packet carries.
func frameCount(packet []byte) int {
	info, err := gopus.ParsePacket(packet)
	if err != nil {
		return 0
	}
	return info.FrameCount
}

// encodeFrames produces n independent single-frame Opus packets from a sine tone.
func encodeFrames(n int) ([][]byte, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		return nil, fmt.Errorf("new encoder: %w", err)
	}

	packets := make([][]byte, n)
	for f := range n {
		pcm := make([]float32, frameSize*channels)
		for i := range pcm {
			t := float64(f*frameSize+i) / sampleRate
			pcm[i] = float32(0.4 * math.Sin(2*math.Pi*440*t))
		}
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			return nil, fmt.Errorf("encode frame %d: %w", f, err)
		}
		packets[f] = pkt
	}
	return packets, nil
}

// decodeAll decodes one (possibly multi-frame) packet and returns the number of
// samples per channel the decoder produced.
func decodeAll(packet []byte) (int, error) {
	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return 0, fmt.Errorf("new decoder: %w", err)
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		return 0, fmt.Errorf("decode: %w", err)
	}
	return n, nil
}
