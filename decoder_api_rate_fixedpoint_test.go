//go:build gopus_fixedpoint

package gopus

import (
	"math"
	"testing"
)

// encodeAPIRateHybridPacketFrameSizeVariant encodes a single Hybrid frame whose
// content varies with the variant seed, so successive frames have distinct
// payloads and exercise cross-frame integer Hybrid state.
func encodeAPIRateHybridPacketFrameSizeVariant(t *testing.T, channels, frameSize, variant int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(EncoderModeHybrid); err != nil {
		t.Fatalf("SetMode(Hybrid): %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(64000 * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		tm := float64(variant*frameSize+i) / sampleRate
		pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*(220+float64(variant)*17)*tm)) +
			0.12*float32(math.Sin(2*math.Pi*(1300+float64(variant)*53)*tm+0.17))
		if channels == 2 {
			pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*(330+float64(variant)*23)*tm+0.09)) +
				0.10*float32(math.Sin(2*math.Pi*(1700+float64(variant)*41)*tm+0.31))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}
