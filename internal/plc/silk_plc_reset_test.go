package plc

import (
	"fmt"
	"slices"
	"testing"
)

func TestConcealSILKResetDiscardsCachedLPC(t *testing.T) {
	for _, fsKHz := range []int{8, 12, 16} {
		t.Run(fmt.Sprintf("%dkHz", fsKHz), func(t *testing.T) {
			dec := makePLCExtDecoder(fsKHz, 5*fsKHz, 4, 20*fsKHz, 10*fsKHz, 0)
			dec.firstFrameAfterReset = true
			clear(dec.outBufQ0)
			clear(dec.slpcQ14)
			for i := range dec.excitation {
				dec.excitation[i] = int32(i%17-8) << 18
			}
			initial := *makePLCStateForDec(dec)
			// Include the unused tail at LPC order 10: silk_PLC_conceal
			// clears the whole saved vector after a side-channel reset.
			for i := range initial.PrevLPCQ12 {
				initial.PrevLPCQ12[i] = int16(16 - i)
			}
			initial.PrevLPCQ12[0] = 1024
			clean := initial
			clear(clean.PrevLPCQ12[:])
			state := initial
			got, want := make([]int16, 20*fsKHz), make([]int16, 20*fsKHz)
			var scratch SILKPLCScratch
			ConcealSILKWithLTPInto(dec, &clean, 0, want, &scratch)
			ConcealSILKWithLTPInto(dec, &state, 0, got, &scratch)
			if !slices.Equal(got, want) || state != clean {
				t.Fatal("reset concealment depends on cached LPC coefficients")
			}
			if state.PrevLPCQ12 != [maxLPCOrder]int16{} {
				t.Fatal("reset retains cached LPC coefficients")
			}
			if allocs := testing.AllocsPerRun(100, func() {
				state = initial
				ConcealSILKWithLTPInto(dec, &state, 0, got, &scratch)
			}); allocs != 0 {
				t.Fatalf("reset concealment allocations=%g want=0", allocs)
			}
			dec.firstFrameAfterReset = false
			state = initial
			ConcealSILKWithLTPInto(dec, &state, 0, got, &scratch)
			if slices.Equal(got, want) {
				t.Fatal("input does not exercise LPC synthesis")
			}
		})
	}
}
