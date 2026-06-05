package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
)

func generateSinePCM(frameSize, channels int, frequency float64) []float64 {
	pcm := make([]float64, frameSize*channels)
	const fs = 48000.0
	for i := range frameSize {
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
	packet, err := encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("WB encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("WB encode returned nil packet")
	}
	check(silk.BandwidthWideband, 16000)

	enc.SetBandwidth(types.BandwidthNarrowband)
	packet, err = encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("NB encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("NB encode returned nil packet")
	}
	check(silk.BandwidthNarrowband, 8000)

	enc.SetBandwidth(types.BandwidthWideband)
	packet, err = encodeTest(enc, pcm, frameSize)
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
	packet, err := encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("stereo WB encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("stereo WB encode returned nil packet")
	}
	check(silk.BandwidthWideband, 16000)

	enc.SetBandwidth(types.BandwidthNarrowband)
	packet, err = encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("stereo NB encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("stereo NB encode returned nil packet")
	}
	check(silk.BandwidthNarrowband, 8000)
}

func TestSILKEncoderForcedBandwidthOverridesMaxBandwidthMono(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(32000)
	enc.SetBandwidth(types.BandwidthFullband)
	frameSize := 960
	pcm := generateSinePCM(frameSize, 1, 440.0)

	tests := []struct {
		name       string
		maxBW      types.Bandwidth
		wantBW     silk.Bandwidth
		wantRateHz int
	}{
		{name: "narrowband", maxBW: types.BandwidthNarrowband, wantBW: silk.BandwidthWideband, wantRateHz: 16000},
		{name: "mediumband", maxBW: types.BandwidthMediumband, wantBW: silk.BandwidthWideband, wantRateHz: 16000},
		{name: "wideband", maxBW: types.BandwidthWideband, wantBW: silk.BandwidthWideband, wantRateHz: 16000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc.SetMaxBandwidth(tc.maxBW)
			packet, err := encodeTest(enc, pcm, frameSize)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			if packet == nil {
				t.Fatal("encode returned nil packet")
			}
			if enc.silkEncoder == nil {
				t.Fatal("silk encoder is nil")
			}
			if got := enc.silkEncoder.Bandwidth(); got != tc.wantBW {
				t.Fatalf("silk bandwidth=%v want %v", got, tc.wantBW)
			}
			if got := enc.silkEncoder.SampleRate(); got != tc.wantRateHz {
				t.Fatalf("silk sample rate=%d want %d", got, tc.wantRateHz)
			}
		})
	}
}

func TestSILKStereoSideEncoderForcedBandwidthOverridesMaxBandwidth(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeSILK)
	enc.SetBitrate(48000)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetMaxBandwidth(types.BandwidthNarrowband)
	frameSize := 960
	pcm := generateSinePCM(frameSize, 2, 330.0)

	packet, err := encodeTest(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if packet == nil {
		t.Fatal("encode returned nil packet")
	}
	if enc.silkEncoder == nil || enc.silkSideEncoder == nil {
		t.Fatal("stereo SILK encoders are not initialized")
	}
	if got := enc.silkEncoder.Bandwidth(); got != silk.BandwidthWideband {
		t.Fatalf("mid silk bandwidth=%v want %v", got, silk.BandwidthWideband)
	}
	if got := enc.silkSideEncoder.Bandwidth(); got != silk.BandwidthWideband {
		t.Fatalf("side silk bandwidth=%v want %v", got, silk.BandwidthWideband)
	}
	if got := enc.silkEncoder.SampleRate(); got != 16000 {
		t.Fatalf("mid silk sample rate=%d want 16000", got)
	}
	if got := enc.silkSideEncoder.SampleRate(); got != 16000 {
		t.Fatalf("side silk sample rate=%d want 16000", got)
	}
}
