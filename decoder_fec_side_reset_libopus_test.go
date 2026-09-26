package gopus

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// These native AMD64 packet histories resume a stereo side channel with a
// missing LBRR subframe. Both decoders receive identical bytes and controls;
// the paired live C oracle supplies every expected PCM sample and final range.
func TestDecodeWithFECSideResetMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)
	data, err := os.ReadFile("testdata/fec_side_reset_packets.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name       string   `json:"name"`
			SampleRate int      `json:"sample_rate"`
			Channels   int      `json:"channels"`
			FrameSize  int      `json:"frame_size"`
			Packets    []string `json:"packets"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 2 {
		t.Fatalf("packet histories=%d want=2", len(fixture.Cases))
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			if len(tc.Packets) < 3 {
				t.Fatal("missing decoder warmup history")
			}
			steps := make([]libopusAPIRateDecodeStep, len(tc.Packets))
			for i, encoded := range tc.Packets {
				packet, err := hex.DecodeString(encoded)
				if err != nil {
					t.Fatalf("packet %d: %v", i, err)
				}
				steps[i] = libopusAPIRateDecodeStep{packet: packet, fec: i == len(steps)-1}
			}
			if !packetHasInBandFEC(t, steps[len(steps)-1].packet) {
				t.Fatal("recovery packet does not carry LBRR")
			}
			want, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(tc.SampleRate, tc.Channels, tc.FrameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "side reset FEC reference", err)
			}
			stride := tc.FrameSize * tc.Channels
			if len(want) != len(steps)*stride || len(ranges) != len(steps) {
				t.Fatalf("reference samples/ranges=%d/%d want=%d/%d", len(want), len(ranges), len(steps)*stride, len(steps))
			}
			dec, err := NewDecoder(DefaultDecoderConfig(tc.SampleRate, tc.Channels))
			if err != nil {
				t.Fatal(err)
			}
			out := make([]float32, stride)
			for i, step := range steps {
				n, err := dec.DecodeWithFEC(step.packet, out, step.fec)
				if err != nil || n != tc.FrameSize {
					t.Fatalf("step %d fec=%v: samples=%d want=%d err=%v", i, step.fec, n, tc.FrameSize, err)
				}
				if dec.FinalRange() != ranges[i] {
					t.Fatalf("step %d fec=%v: range=%08x want=%08x", i, step.fec, dec.FinalRange(), ranges[i])
				}
				for j, sample := range out {
					gotBits, wantBits := math.Float32bits(sample), math.Float32bits(want[i*stride+j])
					if gotBits != wantBits {
						t.Fatalf("step %d fec=%v sample %d: PCM=%08x want=%08x", i, step.fec, j, gotBits, wantBits)
					}
				}
			}
		})
	}
}
