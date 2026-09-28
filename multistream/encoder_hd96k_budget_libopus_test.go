//go:build gopus_qext

package multistream

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMultistreamNativeHD96kBudgetSequenceMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const capacity = 3825
	layout := multistreamHD96kLayout{name: "mono", channels: 1, streams: 1, mapping: []byte{0}}
	for _, frameSize := range []int{240, 1920, 3840} {
		for _, qext := range []bool{false, true} {
			for _, primed := range []bool{false, true} {
				for _, low := range []int{1, 2, 3, 4, 8, 16, 32, 64, 128} {
					t.Run(fmt.Sprintf("frame%d/qext%t/primed%t/budget%d", frameSize, qext, primed, low), func(t *testing.T) {
						budgets := []int{low, capacity}
						if primed {
							budgets = append([]int{capacity}, budgets...)
						}
						frames := makeMultistreamHD96kPCM(frameSize, len(budgets), 1)
						want, _, _, _, _, _ := encodeMultistreamHD96kWithLibopus(t, layout, qext, false, frameSize, capacity, 256000, 10, frames, nil, budgets)
						enc, err := NewEncoder(96000, 1, 1, 0, []byte{0})
						if err != nil {
							t.Fatal(err)
						}
						enc.SetBitrate(256000)
						enc.SetComplexity(10)
						enc.SetVBR(true)
						enc.SetVBRConstraint(true)
						enc.SetQEXT(qext)
						out := make([]byte, capacity)
						for step, budget := range budgets {
							n, err := enc.Encode(frames[step], frameSize, out[:budget])
							if err != nil {
								t.Fatalf("step%d budget%d: %v", step, budget, err)
							}
							if n > budget || !bytes.Equal(out[:n], want[step].packet) {
								t.Fatalf("step%d budget%d packet Go=%x C=%x", step, budget, out[:n], want[step].packet)
							}
							if got := enc.GetFinalRange(); got != want[step].range32 {
								t.Fatalf("step%d range %08x C=%08x", step, got, want[step].range32)
							}
						}
						if allocs := testing.AllocsPerRun(10, func() {
							for step, budget := range budgets {
								if _, err := enc.Encode(frames[step], frameSize, out[:budget]); err != nil {
									panic(err)
								}
							}
						}); allocs != 0 {
							t.Fatalf("warm allocations=%g", allocs)
						}
					})
				}
			}
		}
	}
}
