//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestSmoothFadeResQEXTMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000, 96000} {
		for _, channels := range []int{1, 2} {
			sampleRate, channels := sampleRate, channels
			t.Run(fmt.Sprintf("%dhz/%dch", sampleRate, channels), func(t *testing.T) {
				overlap := sampleRate / 400
				count := overlap * channels
				in1, in2 := make([]int32, count), make([]int32, count)
				state := uint32(0x9e3779b9 + sampleRate + channels)
				for i := 0; i < count; i++ {
					state = state*1664525 + 1013904223
					in1[i] = int32(state>>1) - 1<<30
					state = state*1664525 + 1013904223
					in2[i] = int32(state>>1) - 1<<30
				}
				want, err := libopustest.ProbeFixedQEXTSmoothFade(libopustest.FixedQEXTSmoothFadeParams{
					SampleRate: sampleRate,
					Channels:   channels,
					Overlap:    overlap,
					In1:        in1,
					In2:        in2,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "selected fixed-QEXT smooth fade", err)
					return
				}
				got := make([]int32, count)
				SmoothFadeResQEXT(in1, in2, got, overlap, channels, sampleRate)
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("SmoothFadeResQEXT[%d]=%d C=%d", i, got[i], want[i])
					}
				}

				aliased := append([]int32(nil), in1...)
				SmoothFadeResQEXT(aliased, in2, aliased, overlap, channels, sampleRate)
				for i := range want {
					if aliased[i] != want[i] {
						t.Fatalf("in-place SmoothFadeResQEXT[%d]=%d C=%d", i, aliased[i], want[i])
					}
				}

				SmoothFadeResQEXT(in1, in2, got, overlap, channels, sampleRate)
				allocs := testing.AllocsPerRun(50, func() {
					SmoothFadeResQEXT(in1, in2, got, overlap, channels, sampleRate)
				})
				if allocs != 0 {
					t.Fatalf("SmoothFadeResQEXT allocated %g times/call", allocs)
				}
			})
		}
	}
}
