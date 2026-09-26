package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestCELTSilenceDecodeMatchesLibopusFloatBits(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, sampleRate := range []int{48000, 24000, 16000, 12000, 8000} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("rate=%d/channels=%d", sampleRate, channels), func(t *testing.T) {
				var cases []libopustest.DecodeDiffCase
				frameSizes := []int{sampleRate * 25 / 10000, sampleRate * 5 / 1000, sampleRate / 100, sampleRate / 50}
				for lm, frameSize := range frameSizes {
					cfg := byte(28 + lm)
					toc := cfg << 3
					if channels == 2 {
						toc |= 4
					}
					cases = append(cases, libopustest.DecodeDiffCase{
						Packet:    []byte{toc, 0xff, 0xfe},
						Format:    libopustest.DecodeDiffFormatFloat32,
						FrameSize: uint32(frameSize),
					})
				}
				want, err := libopustest.ProbeDecodeDiff(sampleRate, channels, cases)
				if err != nil {
					t.Fatalf("libopus silence decode oracle: %v", err)
				}
				if len(want) != len(cases) {
					t.Fatalf("oracle cases=%d want %d", len(want), len(cases))
				}
				for i, c := range cases {
					t.Run(fmt.Sprintf("duration-index=%d", i), func(t *testing.T) {
						frameSize := int(c.FrameSize)
						config := DefaultDecoderConfig(sampleRate, channels)
						dec, err := NewDecoder(config)
						if err != nil {
							t.Fatalf("NewDecoder: %v", err)
						}
						got := make([]float32, frameSize*channels)
						n, err := dec.Decode(c.Packet, got)
						if err != nil {
							t.Fatalf("Decode: %v", err)
						}
						if want[i].Code != int32(frameSize) {
							t.Fatalf("libopus decoded samples=%d want %d", want[i].Code, frameSize)
						}
						if n != frameSize {
							t.Fatalf("gopus decoded samples=%d want %d", n, frameSize)
						}
						assertFloat32SliceBits(t, "silence pcm", got, want[i].Float32())
					})
				}
			})
		}
	}
}

func assertFloat32SliceBits(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%08x %.10g want %08x %.10g", label, i, math.Float32bits(got[i]), got[i], math.Float32bits(want[i]), want[i])
		}
	}
}
