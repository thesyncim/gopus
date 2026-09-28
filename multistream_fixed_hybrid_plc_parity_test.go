//go:build gopus_fixed_point

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	mspkg "github.com/thesyncim/gopus/multistream"
)

func TestMultistreamFixedHybridPLCRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{480, 960} {
			t.Run(fmt.Sprintf("ch%d/fs%d", channels, frameSize), func(t *testing.T) {
				streams, coupled := 1, 0
				mapping := []byte{0}
				if channels == 2 {
					coupled = 1
					mapping = []byte{0, 1}
				}
				seed := encodeAPIRateHybridPacketFrameSize(t, channels, frameSize)
				recovery := encodeAPIRateHybridPacketFrameSizeVariant(t, channels, frameSize, frameSize+19)
				sequence := [][]byte{seed, nil, nil, recovery}
				want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, frameSize, mapping, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed Hybrid PLC/recovery int24", err)
					return
				}
				dec, err := mspkg.NewDecoder(sampleRate, channels, streams, coupled, mapping)
				if err != nil {
					t.Fatal(err)
				}
				got24 := make([]int32, 0, len(want24))
				for i, packet := range sequence {
					out, err := dec.DecodeToInt24(packet, frameSize)
					if err != nil || len(out) != frameSize*channels {
						t.Fatalf("packet %d DecodeToInt24 samples=%d err=%v, want %d", i, len(out), err, frameSize*channels)
					}
					got24 = append(got24, out...)
				}
				if len(got24) != len(want24) {
					t.Fatalf("Hybrid PLC and recovery int24 sample count=%d, want %d", len(got24), len(want24))
				}
				for i := range got24 {
					if got24[i] != want24[i] {
						t.Fatalf("Hybrid PLC and recovery int24 differs at sample %d: Go=%d C=%d", i, got24[i], want24[i])
					}
				}
			})
		}
	}
}
