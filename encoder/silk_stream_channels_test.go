package encoder_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func TestSILKForcedMonoStereoAPIUsesMonoPacket(t *testing.T) {
	const frameSize = 960
	enc := encoder.NewEncoder(48000, 2)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(16000)
	enc.SetBitrateMode(encoder.ModeVBR)
	enc.SetForceChannels(1)

	pcm := make([]float64, frameSize*2)
	for i := 0; i < frameSize; i++ {
		pcm[2*i] = 0.2 * math.Sin(2*math.Pi*440*float64(i)/48000)
		pcm[2*i+1] = 0.2 * math.Sin(2*math.Pi*660*float64(i)/48000)
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode() error: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode() returned empty packet")
	}
	toc := gopus.ParseTOC(packet[0])
	if toc.Mode != gopus.ModeSILK {
		t.Fatalf("TOC mode = %v, want %v", toc.Mode, gopus.ModeSILK)
	}
	if toc.Stereo {
		t.Fatal("forced-mono SILK packet has stereo TOC")
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder() error: %v", err)
	}
	out := make([]float32, frameSize*2)
	n, err := dec.Decode(packet, out)
	if err != nil {
		t.Fatalf("Decode() error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode() samples = %d, want %d", n, frameSize)
	}
}

func TestCELTForcedMonoStereoAPIUsesMonoPacket(t *testing.T) {
	const frameSize = 960
	enc := encoder.NewEncoder(48000, 2)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(32000)
	enc.SetBitrateMode(encoder.ModeVBR)
	enc.SetForceChannels(1)

	pcm := make([]float64, frameSize*2)
	for i := 0; i < frameSize; i++ {
		pcm[2*i] = 0.18 * math.Sin(2*math.Pi*330*float64(i)/48000)
		pcm[2*i+1] = 0.14 * math.Sin(2*math.Pi*550*float64(i)/48000)
	}

	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode() error: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode() returned empty packet")
	}
	toc := gopus.ParseTOC(packet[0])
	if toc.Mode != gopus.ModeCELT {
		t.Fatalf("TOC mode = %v, want %v", toc.Mode, gopus.ModeCELT)
	}
	if toc.Stereo {
		t.Fatal("forced-mono CELT packet has stereo TOC")
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder() error: %v", err)
	}
	out := make([]float32, frameSize*2)
	n, err := dec.Decode(packet, out)
	if err != nil {
		t.Fatalf("Decode() error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode() samples = %d, want %d", n, frameSize)
	}
}

func TestHybridForcedMonoStereoAPIUsesMonoPacket(t *testing.T) {
	for _, frameSize := range []int{960, 1920} {
		t.Run(fmt.Sprintf("%d", frameSize), func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 2)
			enc.SetMode(encoder.ModeHybrid)
			enc.SetBandwidth(types.BandwidthSuperwideband)
			enc.SetBitrate(36000)
			enc.SetBitrateMode(encoder.ModeVBR)
			enc.SetForceChannels(1)

			pcm := make([]float64, frameSize*2)
			for i := 0; i < frameSize; i++ {
				pcm[2*i] = 0.16 * math.Sin(2*math.Pi*420*float64(i)/48000)
				pcm[2*i+1] = 0.12 * math.Sin(2*math.Pi*690*float64(i)/48000)
			}

			packet, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode() error: %v", err)
			}
			if len(packet) == 0 {
				t.Fatal("Encode() returned empty packet")
			}
			toc := gopus.ParseTOC(packet[0])
			if toc.Mode != gopus.ModeHybrid {
				t.Fatalf("TOC mode = %v, want %v", toc.Mode, gopus.ModeHybrid)
			}
			if toc.Stereo {
				t.Fatal("forced-mono Hybrid packet has stereo TOC")
			}

			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
			if err != nil {
				t.Fatalf("NewDecoder() error: %v", err)
			}
			out := make([]float32, frameSize*2)
			n, err := dec.Decode(packet, out)
			if err != nil {
				t.Fatalf("Decode() error: %v", err)
			}
			if n != frameSize {
				t.Fatalf("Decode() samples = %d, want %d", n, frameSize)
			}
		})
	}
}
