package multistream

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestProjectionDecodeInt24MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 4
		frameCount = 6
	)
	mapping := trivialMapping(channels)
	for _, frameSize := range []int{480, 960} {
		for _, bitrate := range []int{96000, 128000, 192000, 256000} {
			t.Run(fmt.Sprintf("frame_%d/bitrate_%d", frameSize, bitrate), func(t *testing.T) {
				ref := projectionDecodeRef(t, channels, frameSize, frameCount, bitrate)
				if !allPerStreamCELT(ref.packets, ref.streams) {
					t.Fatalf("expected all-CELT stream at frame=%d bitrate=%d", frameSize, bitrate)
				}
				dec, err := NewProjectionDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, ref.demixing)
				if err != nil {
					t.Fatalf("NewProjectionDecoder: %v", err)
				}
				var got []int32
				for i, packet := range ref.packets {
					frame, err := dec.DecodeToInt24(packet, frameSize)
					if err != nil {
						t.Fatalf("frame %d DecodeToInt24: %v", i, err)
					}
					got = append(got, frame...)
				}
				want, err := decodeWithLibopusReferencePacketsInt24Gain(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, 0, mapping, ref.demixing, ref.packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "projection int24 reference decode", err)
				}
				if len(got) != len(want) {
					t.Fatalf("sample count mismatch Go=%d C=%d", len(got), len(want))
				}
				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("sample %d int24 Go/C=%d/%d", i, got[i], want[i])
					}
				}
			})
		}
	}
}
