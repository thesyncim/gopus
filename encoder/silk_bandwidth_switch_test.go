package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func generateSinePCM(frameSize, channels int, frequency float64) []float64 {
	pcm := make([]float64, frameSize*channels)
	const fs = 48000.0
	for i := 0; i < frameSize; i++ {
		sample := 0.25 * math.Sin(2*math.Pi*frequency*float64(i)/fs)
		if channels == 2 {
			pcm[2*i] = sample
			pcm[2*i+1] = sample
		} else {
			pcm[i] = sample
		}
	}
	return pcm
}

func TestSILKEncoderReconfiguresOnBandwidthChangeMono(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(32000)
	frameSize := 960
	pcm := generateSinePCM(frameSize, 1, 440.0)

	check := func(expectBW silk.Bandwidth, expectRate int) {
		if enc.silkEncoder == nil {
			t.Fatal("silk encoder is nil")
		}
		if got := enc.silkEncoder.Bandwidth(); got != expectBW {
			t.Fatalf("unexpected silk bandwidth: got %v want %v", got, expectBW)
		}
		if got := enc.silkEncoder.SampleRate(); got != expectRate {
			t.Fatalf("unexpected silk sample rate: got %d want %d", got, expectRate)
		}
	}

	enc.SetBandwidth(types.BandwidthWideband)
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("WB encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("WB encode returned nil packet")
	}
	check(silk.BandwidthWideband, 16000)

	enc.SetBandwidth(types.BandwidthNarrowband)
	packet, err = enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("NB encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("NB encode returned nil packet")
	}
	check(silk.BandwidthNarrowband, 8000)

	enc.SetBandwidth(types.BandwidthWideband)
	packet, err = enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("WB re-encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("WB re-encode returned nil packet")
	}
	check(silk.BandwidthWideband, 16000)
}

func TestSILKEncoderReconfiguresOnBandwidthChangeStereo(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(48000)
	frameSize := 960
	pcm := generateSinePCM(frameSize, 2, 330.0)

	check := func(expectBW silk.Bandwidth, expectRate int) {
		if enc.silkEncoder == nil {
			t.Fatal("mid silk encoder is nil")
		}
		if enc.silkSideEncoder == nil {
			t.Fatal("side silk encoder is nil")
		}
		if got := enc.silkEncoder.Bandwidth(); got != expectBW {
			t.Fatalf("unexpected mid silk bandwidth: got %v want %v", got, expectBW)
		}
		if got := enc.silkSideEncoder.Bandwidth(); got != expectBW {
			t.Fatalf("unexpected side silk bandwidth: got %v want %v", got, expectBW)
		}
		if got := enc.silkEncoder.SampleRate(); got != expectRate {
			t.Fatalf("unexpected mid silk sample rate: got %d want %d", got, expectRate)
		}
		if got := enc.silkSideEncoder.SampleRate(); got != expectRate {
			t.Fatalf("unexpected side silk sample rate: got %d want %d", got, expectRate)
		}
	}

	enc.SetBandwidth(types.BandwidthWideband)
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("stereo WB encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("stereo WB encode returned nil packet")
	}
	check(silk.BandwidthWideband, 16000)

	enc.SetBandwidth(types.BandwidthNarrowband)
	packet, err = enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("stereo NB encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("stereo NB encode returned nil packet")
	}
	check(silk.BandwidthNarrowband, 8000)
}

