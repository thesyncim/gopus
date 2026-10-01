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

func TestSILKPLCUpdateResetsBeforeGoodFrameAtNewRate(t *testing.T) {
	state := NewSILKPLCState()
	state.FsKHz = 16
	state.PitchLQ8 = 3456 << 8
	state.PrevGainQ16 = [2]int32{3 << 16, 4 << 16}

	const (
		fsKHz      = 12
		nbSubfr    = 4
		subfrLen   = 60
		frameLen   = nbSubfr * subfrLen
		initialLag = frameLen << 7
	)
	pitchL := []int32{120, 120, 120, 120}
	ltpCoefQ14 := make([]int16, nbSubfr*ltpOrder)
	gainsQ16 := []int32{1 << 16, 2 << 16, 3 << 16, 4 << 16}
	lpcQ12 := make([]int16, 10)

	// PLC.c silk_PLC runs silk_PLC_Reset before silk_PLC_update on a rate
	// change. A voiced update with zero LTP gain leaves pitchL_Q8 at the reset
	// frameLength<<7 value, rather than retaining the old-rate lag.
	state.UpdateFromGoodFrame(2, pitchL, ltpCoefQ14, 0, gainsQ16, lpcQ12, fsKHz, nbSubfr, subfrLen)
	if state.FsKHz != fsKHz || state.PitchLQ8 != initialLag {
		t.Fatalf("rate-change update FsKHz=%d PitchLQ8=%d, want FsKHz=%d PitchLQ8=%d", state.FsKHz, state.PitchLQ8, fsKHz, initialLag)
	}
	if state.PrevGainQ16 != [2]int32{3 << 16, 4 << 16} {
		t.Fatalf("good-frame update gains=%v, want last two frame gains", state.PrevGainQ16)
	}
}
