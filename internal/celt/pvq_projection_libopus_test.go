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
				for _, mutate := range []bool{false, true} {
					t.Run(fmt.Sprintf("mutate%t", mutate), func(t *testing.T) {
						var iy []int32
						var sign []byte
						var y, abs []float32
						input := make([]float32, len(x))
						call := func() ([]int32, float32) {
							copy(input, x)
							return opPVQSearchScratchNormWithInputMutation(input, k, &iy, &sign, &y, &abs, mutate)
						}
						gotIY, gotYY := call()
						if !equalInt32Slices(gotIY, wantIY) || math.Float32bits(gotYY) != math.Float32bits(wantYY) {
							t.Fatalf("Go pulses=%v yy=%08x C pulses=%v yy=%08x", gotIY, math.Float32bits(gotYY), wantIY, math.Float32bits(wantYY))
						}
						if allocs := testing.AllocsPerRun(20, func() { _, _ = call() }); allocs != 0 {
							t.Fatalf("warm allocations=%g", allocs)
						}
					})
				}
			})
		}
	}
}

func TestPVQSearchInputMutationEdgeCasesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	probe := libopustest.ProbeCELTPVQSearchFloat
	if useX86PVQSearchSSE2 {
		probe = libopustest.ProbeCELTPVQSearchFloatSSE2
	}
	negativeZero := math.Float32frombits(1 << 31)
	for caseIndex, source := range [][]float32{
		{0, negativeZero, 0, negativeZero},
		{-0.25, 0.5, -0.75, 0, 0.25, -0.125, 0.625},
		{math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32, 0, negativeZero},
	} {
		for _, k := range []int{1, 2, 40} {
			wantYY, wantIY, err := probe(source, k)
			if err != nil {
				libopustest.HelperUnavailable(t, "PVQ input mutation edge cases", err)
				return
			}
			for _, mutate := range []bool{false, true} {
				input := append([]float32(nil), source...)
				gotIY, gotYY := opPVQSearchScratchNormWithInputMutation(input, k, nil, nil, nil, nil, mutate)
				if !equalInt32Slices(gotIY, wantIY) || math.Float32bits(gotYY) != math.Float32bits(wantYY) {
					t.Fatalf("case%d k=%d mutate=%t: Go pulses=%v yy=%08x C pulses=%v yy=%08x", caseIndex, k, mutate, gotIY, math.Float32bits(gotYY), wantIY, math.Float32bits(wantYY))
				}
			}
		}
	}
}
