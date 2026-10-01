//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestGainFadeResQEXTMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	var cases []libopustest.FixedQEXTGainFadeCase
	for _, sampleRate := range [...]int{8000, 12000, 16000, 24000, 48000, 96000} {
		for _, channels := range [...]int{1, 2} {
			frameSize := sampleRate / 50
			for _, gains := range [...]struct{ g1, g2 int16 }{{0, 32767}, {32767, 0}} {
				samples := make([]int32, frameSize*channels)
				state := uint32(0x6d2b79f5 + sampleRate*17 + channels*101 + int(gains.g1))
				for i := range samples {
					state ^= state << 13
					state ^= state >> 17
					state ^= state << 5
					samples[i] = int32(state)
				}
				cases = append(cases, libopustest.FixedQEXTGainFadeCase{
					SampleRate: sampleRate,
					Channels:   channels,
					FrameSize:  frameSize,
					G1:         gains.g1,
					G2:         gains.g2,
					Samples:    samples,
				})
			}
		}
	}

	want, err := libopustest.ProbeFixedQEXTGainFade(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected fixed-QEXT gain fade", err)
		return
	}
	for caseIndex, c := range cases {
		got := append([]int32(nil), c.Samples...)
		GainFadeResQEXT(got, c.Channels, c.G1, c.G2, c.SampleRate)
		for i := range want[caseIndex] {
			if got[i] != want[caseIndex][i] {
				t.Fatalf("rate=%d channels=%d gains=%d->%d sample=%d: Go=%d C=%d",
					c.SampleRate, c.Channels, c.G1, c.G2, i, got[i], want[caseIndex][i])
			}
		}
	}
}

func TestGainFadeResQEXTAllocs(t *testing.T) {
	for _, sampleRate := range [...]int{8000, 12000, 16000, 24000, 48000, 96000} {
		for _, channels := range [...]int{1, 2} {
			t.Run(fmt.Sprintf("%dHz/%dch", sampleRate, channels), func(t *testing.T) {
				samples := make([]int32, sampleRate/50*channels)
				for i := range samples {
					samples[i] = int32((i*7919)%(1<<20) - (1 << 19))
				}
				for range 8 {
					GainFadeResQEXT(samples, channels, 32767, 24575, sampleRate)
				}
				if allocs := testing.AllocsPerRun(100, func() {
					GainFadeResQEXT(samples, channels, 32767, 24575, sampleRate)
				}); allocs != 0 {
					t.Fatalf("GainFadeResQEXT allocs/op = %.2f, want 0", allocs)
				}
			})
		}
	}
}
