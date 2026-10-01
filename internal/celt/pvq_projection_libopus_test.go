package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPVQProjectionRoundingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	probe := libopustest.ProbeCELTPVQSearchFloat
	if useX86PVQSearchSSE2 {
		probe = libopustest.ProbeCELTPVQSearchFloatSSE2
	}
	// This normalized six-bin shape comes from a live 2.5 ms CELT packet.
	bits := []uint32{0x3deb418e, 0x3e440f79, 0x3f0af847, 0x3ce14d80, 0x3e43c501, 0x3e5c2735}
	for _, n := range []int{3, 4, 5, 6, 7, 15, 16, 17} {
		for _, k := range []int{40, 72, 256} {
			t.Run(fmt.Sprintf("n%d-k%d", n, k), func(t *testing.T) {
				x := make([]float32, n)
				for i := range x {
					x[i] = math.Float32frombits(bits[i%len(bits)])
				}
				wantYY, wantIY, err := probe(x, k)
				if err != nil {
					libopustest.HelperUnavailable(t, "PVQ projection", err)
					return
				}
				var iy []int32
				var sign []byte
				var y, abs []float32
				call := func() ([]int32, float32) { return opPVQSearchScratchNorm(x, k, &iy, &sign, &y, &abs) }
				gotIY, gotYY := call()
				if !equalInt32Slices(gotIY, wantIY) || math.Float32bits(gotYY) != math.Float32bits(wantYY) {
					t.Fatalf("Go pulses=%v yy=%08x C pulses=%v yy=%08x", gotIY, math.Float32bits(gotYY), wantIY, math.Float32bits(wantYY))
				}
				if allocs := testing.AllocsPerRun(20, func() { _, _ = call() }); allocs != 0 {
					t.Fatalf("warm allocations=%g", allocs)
				}
			})
		}
	}
}
