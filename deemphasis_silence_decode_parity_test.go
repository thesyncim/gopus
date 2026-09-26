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

// Received CELT silence carries synthesis overlap and filter memory from the
// preceding signal. Compare the subsequent recovery with the same C history.
func TestCELTReceivedSilenceHistoryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, rate := range []int{48000, 16000} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("rate%d/ch%d", rate, channels), func(t *testing.T) {
				packet := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
				silence := []byte{packet[0] & 0xfc, 0xff, 0xfe}
				steps := []libopusAPIRateDecodeStep{{packet: packet}, {packet: silence}, {packet: silence}, {packet: packet}}
				frame := rate / 50
				want, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(rate, channels, frame, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "CELT received silence history", err)
				}
				stride := frame * channels
				if len(want) != len(steps)*stride || len(ranges) != len(steps) {
					t.Fatalf("C samples/ranges=%d/%d", len(want), len(ranges))
				}
				tail := false
				for _, v := range want[stride : 2*stride] {
					if math.Abs(float64(v)) > 1e-10 {
						tail = true
						break
					}
				}
				if !tail {
					t.Fatal("received silence does not exercise nonzero synthesis history")
				}
				dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
				if err != nil {
					t.Fatal(err)
				}
				out := make([]float32, stride)
				for i, step := range steps {
					n, err := dec.Decode(step.packet, out)
					if err != nil || n != frame {
						t.Fatalf("step%d samples=%d want=%d err=%v", i, n, frame, err)
					}
					if dec.FinalRange() != ranges[i] {
						t.Fatalf("step%d range=%08x want=%08x", i, dec.FinalRange(), ranges[i])
					}
					assertFloat32SliceBits(t, fmt.Sprintf("step%d", i), out, want[i*stride:(i+1)*stride])
				}
			})
		}
	}
}
