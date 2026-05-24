package encoder

import (
	"bytes"
	"math"
	"testing"
)

func TestEncodeFloat32MatchesFloat32AwareFloat64Bridge(t *testing.T) {
	const frameSize = 960

	pcm32 := make([]float32, frameSize)
	for i := range pcm32 {
		tm := float64(i) / 48000.0
		pcm32[i] = float32(0.35*math.Sin(2*math.Pi*440*tm) + 0.07*math.Sin(2*math.Pi*1200*tm))
	}
	pcm64 := make([]float64, len(pcm32))
	for i, v := range pcm32 {
		pcm64[i] = float64(v)
	}

	float32Enc := NewEncoder(48000, 1)
	float32Enc.SetMode(ModeCELT)
	float32Enc.SetBitrateMode(ModeCBR)
	float32Enc.SetBitrate(64000)

	bridgeEnc := NewEncoder(48000, 1)
	bridgeEnc.SetMode(ModeCELT)
	bridgeEnc.SetBitrateMode(ModeCBR)
	bridgeEnc.SetBitrate(64000)
	bridgeEnc.SetFloatInputFrame(pcm32)
	defer bridgeEnc.ClearFloatInputFrame()

	got, err := float32Enc.EncodeFloat32(pcm32, frameSize)
	if err != nil {
		t.Fatalf("EncodeFloat32 error: %v", err)
	}
	want, err := bridgeEnc.EncodeWithAnalysisMaxBytes(pcm64, frameSize, pcm64, maxSilkPacketBytes)
	if err != nil {
		t.Fatalf("EncodeWithAnalysisMaxBytes error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeFloat32 packet mismatch with float32-aware bridge: got %d bytes want %d", len(got), len(want))
	}
	if float32Enc.floatInputFrame != nil || float32Enc.floatInputExact {
		t.Fatal("EncodeFloat32 did not clear per-call float32 input state")
	}
}

func TestEncodeFloat32WithAnalysisValidatesAnalysisFrame(t *testing.T) {
	enc := NewEncoder(48000, 1)
	pcm := make([]float32, 960)

	if _, err := enc.EncodeFloat32WithAnalysisMaxBytes(pcm, 960, pcm[:959], maxSilkPacketBytes); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeFloat32WithAnalysisMaxBytes short analysis error=%v want %v", err, ErrInvalidFrameSize)
	}
}
