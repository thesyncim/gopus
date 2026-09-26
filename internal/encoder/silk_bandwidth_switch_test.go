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
		checkSILKBandwidth(t, enc, expectBW, expectRate)
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
		checkSILKBandwidth(t, enc, expectBW, expectRate)
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
			checkSILKBandwidth(t, enc, tc.wantBW, tc.wantRateHz)
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
	checkSILKBandwidth(t, enc, silk.BandwidthWideband, 16000)
	if got := enc.silkMode.NChannelsInternal; got != 2 {
		t.Fatalf("SILK coded %d channels, want 2", got)
	}
}

// checkSILKBandwidth checks the bandwidth the SILK encoder codes at and the
// internal sampling rate silk_Encode reported for the last packet.
func checkSILKBandwidth(t *testing.T, enc *Encoder, wantBW silk.Bandwidth, wantRateHz int) {
	t.Helper()
	if enc.silk == nil {
		t.Fatal("silk encoder is nil")
	}
	if got := enc.silk.Bandwidth(); got != wantBW {
		t.Fatalf("silk bandwidth=%v want %v", got, wantBW)
	}
	if got := int(enc.silkMode.InternalSampleRate); got != wantRateHz {
		t.Fatalf("silk internal sample rate=%d want %d", got, wantRateHz)
	}
}
