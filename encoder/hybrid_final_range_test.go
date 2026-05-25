package encoder_test

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func TestHybridTransitionFinalRangeIncludesRedundancy(t *testing.T) {
	const frameSize = 960

	tests := []struct {
		name  string
		modes []encoder.Mode
	}{
		{
			name:  "celt_to_hybrid",
			modes: []encoder.Mode{encoder.ModeCELT, encoder.ModeHybrid},
		},
		{
			name:  "hybrid_to_celt",
			modes: []encoder.Mode{encoder.ModeHybrid, encoder.ModeCELT},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(64000)
			enc.SetBitrateMode(encoder.ModeCBR)

			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatalf("NewDecoder() error: %v", err)
			}
			out := make([]float32, frameSize)

			for i, mode := range tc.modes {
				enc.SetMode(mode)
				packet, err := encodeTest(enc, hybridFinalRangePCM(i, frameSize), frameSize)
				if err != nil {
					t.Fatalf("Encode(%d) error: %v", i, err)
				}
				if len(packet) == 0 {
					t.Fatalf("Encode(%d) returned empty packet", i)
				}
				if n, err := dec.Decode(packet, out); err != nil {
					t.Fatalf("Decode(%d) error: %v", i, err)
				} else if n != frameSize {
					t.Fatalf("Decode(%d) samples = %d, want %d", i, n, frameSize)
				}
				if got, want := enc.FinalRange(), dec.FinalRange(); got != want {
					t.Fatalf("frame %d final range = 0x%08x, want decoder 0x%08x", i, got, want)
				}
			}
		})
	}
}

func hybridFinalRangePCM(frame, frameSize int) []float64 {
	pcm := make([]float64, frameSize)
	phase := float64(frame * frameSize)
	for i := range pcm {
		t := (phase + float64(i)) / 48000
		pcm[i] = 0.22*math.Sin(2*math.Pi*440*t) + 0.08*math.Sin(2*math.Pi*880*t)
	}
	return pcm
}
