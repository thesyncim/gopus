//go:build gopus_fixed_point

package encoder

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/silk"
)

func TestFixedHPCutoffResMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cutoffs := []int{60, 73, 89, 100}
	q8Boundaries := []int32{
		(120 << 8) + 127, (120 << 8) + 128, (120 << 8) + 129,
		-(120 << 8) - 127, -(120 << 8) - 128, -(120 << 8) - 129,
		(32767 << 8) + 127, (32767 << 8) + 128, (32767 << 8) + 129,
		-(32768 << 8) - 127, -(32768 << 8) - 128, -(32768 << 8) - 129,
	}
	type testCase struct {
		name   string
		fs     int
		ch     int
		mem    [4]int32
		input  [][]int32
		output [][]int32
		states [][4]int32
	}
	var cases []testCase
	for _, fs := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, seeded := range []bool{false, true} {
				tc := testCase{
					name: fmt.Sprintf("fs%d/ch%d/seeded%t", fs, channels, seeded),
					fs:   fs, ch: channels,
					input: make([][]int32, len(cutoffs)),
					mem:   [4]int32{1 << 18, -(1 << 17), 1 << 16, -(1 << 15)},
				}
				if !seeded {
					tc.mem = [4]int32{}
				}
				frameSize := fs / 50
				for frame := range tc.input {
					tc.input[frame] = make([]int32, frameSize*channels)
					for i := range tc.input[frame] {
						if i < len(q8Boundaries) {
							tc.input[frame][i] = q8Boundaries[i]
							continue
						}
						x := float32(.72*math.Sin(float64(frame*len(tc.input[frame])+i)*.017) +
							.43*math.Sin(float64(i)*.231+float64(frame)*.41))
						if (i+frame)%211 == 0 {
							x = 1.35
						} else if (i+frame)%223 == 0 {
							x = -1.35
						}
						tc.input[frame][i] = fixedFloatToRes(x)
					}
				}
				goState := tc.mem
				for frame := range tc.input {
					out := make([]int32, len(tc.input[frame]))
					silk.HPCutoffRes24(tc.input[frame], out, &goState, int32(fs), int32(channels), int32(cutoffs[frame]))
					tc.output = append(tc.output, out)
					tc.states = append(tc.states, goState)
				}
				cases = append(cases, tc)
			}
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, states, err := libopustest.ProbeFixedHPCutoff(tc.input, tc.mem, tc.fs, tc.ch, cutoffs)
			if err != nil {
				t.Fatal(err)
			}
			for frame := range tc.input {
				for i, got := range tc.output[frame] {
					if got != want[frame][i] {
						t.Fatalf("frame%d sample%d Q8=%d C=%d", frame, i, got, want[frame][i])
					}
				}
				if tc.states[frame] != states[frame] {
					t.Fatalf("frame%d state=%v C=%v", frame, tc.states[frame], states[frame])
				}
			}
		})
	}

	input := cases[0].input[0]
	output := make([]int32, len(input))
	state := cases[0].mem
	for range 3 {
		silk.HPCutoffRes24(input, output, &state, int32(cases[0].fs), int32(cases[0].ch), int32(cutoffs[0]))
	}
	allocs := testing.AllocsPerRun(50, func() {
		silk.HPCutoffRes24(input, output, &state, int32(cases[0].fs), int32(cases[0].ch), int32(cutoffs[0]))
	})
	if allocs != 0 {
		t.Fatalf("fixed hp cutoff warm allocations=%g, want 0", allocs)
	}
}
