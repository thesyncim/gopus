package encoder_test

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func fillSpeechLikePCM(pcm []float64, startSample, frameSize, channels int, seed *uint32) {
	for i := 0; i < frameSize; i++ {
		t := float64(startSample+i) / 48000.0
		voiced := 0.35*math.Sin(2*math.Pi*150*t) + 0.15*math.Sin(2*math.Pi*300*t)
		*seed = *seed*1664525 + 1013904223
		noise := 0.15 * (float64((*seed>>9)&0x3FF)/512.0 - 1.0)
		s := voiced + noise
		if channels == 1 {
			pcm[i] = s
		} else {
			pcm[i*channels] = s
			pcm[i*channels+1] = s
		}
	}
}

// TestHybridVBRBitrateBudget ensures hybrid VBR stays near the target bitrate.
func TestHybridVBRBitrateBudget(t *testing.T) {
	const (
		sampleRate = 48000
		frameSize  = 960
		channels   = 2
		bitrate    = 64000
		frames     = 100
	)

	enc := encoder.NewEncoder(sampleRate, channels)
	enc.SetMode(encoder.ModeHybrid)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(bitrate)
	enc.SetBitrateMode(encoder.ModeVBR)
	enc.SetSignalType(types.SignalVoice)

	pcm := make([]float64, frameSize*channels)
	var totalBytes int
	seed := uint32(12345)
	for frame := 0; frame < frames; frame++ {
		fillSpeechLikePCM(pcm, frame*frameSize, frameSize, channels, &seed)
		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("encode frame %d: %v", frame, err)
		}
		if packet == nil {
			t.Fatalf("encode frame %d: empty packet", frame)
		}
		totalBytes += len(packet)
	}

	avgBytes := float64(totalBytes) / float64(frames)
	expected := float64(bitrate*frameSize) / float64(sampleRate*8)
	maxAllowed := expected * 1.3
	if avgBytes > maxAllowed {
		t.Fatalf("avg bytes %.1f exceed target %.1f (max %.1f)", avgBytes, expected, maxAllowed)
	}
	if avgBytes < expected*0.6 {
		t.Fatalf("avg bytes %.1f unexpectedly low vs target %.1f", avgBytes, expected)
	}
}

func TestCELTLongFrameVBRBitrateBudget(t *testing.T) {
	tests := []struct {
		name      string
		frameSize int
	}{
		{name: "40ms", frameSize: 1920},
		{name: "60ms", frameSize: 2880},
	}

	const (
		sampleRate = 48000
		channels   = 2
		bitrate    = 64000
		frames     = 60
	)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := encoder.NewEncoder(sampleRate, channels)
			enc.SetMode(encoder.ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(bitrate)
			enc.SetBitrateMode(encoder.ModeVBR)
			enc.SetSignalType(types.SignalMusic)

			pcm := make([]float64, tc.frameSize*channels)
			var totalBytes int
			seed := uint32(12345)
			for frame := 0; frame < frames; frame++ {
				fillSpeechLikePCM(pcm, frame*tc.frameSize, tc.frameSize, channels, &seed)
				packet, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("encode frame %d: %v", frame, err)
				}
				if packet == nil {
					t.Fatalf("encode frame %d: empty packet", frame)
				}
				totalBytes += len(packet)
			}

			avgBytes := float64(totalBytes) / float64(frames)
			expected := float64(bitrate*tc.frameSize) / float64(sampleRate*8)
			maxAllowed := expected * 1.35
			minAllowed := expected * 0.70
			if avgBytes > maxAllowed {
				t.Fatalf("avg bytes %.1f exceed target %.1f (max %.1f)", avgBytes, expected, maxAllowed)
			}
			if avgBytes < minAllowed {
				t.Fatalf("avg bytes %.1f below target %.1f (min %.1f)", avgBytes, expected, minAllowed)
			}
		})
	}
}
