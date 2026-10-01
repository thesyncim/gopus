//go:build gopus_fixed_point && gopus_qext

package encoder

import (
	"slices"
	"testing"
)

func TestFixedQEXTInputBypassesDCHighpassWithoutAdvancingMemory(t *testing.T) {
	e := NewEncoder(48000, 2)
	e.SetQEXT(true)
	e.fixedInputActive = true
	e.fixedRawRes = []int32{1234567, -2345678, 3456789, -4567890}
	e.fixedFiltered = make([]int32, len(e.fixedRawRes))
	e.fixedHPMem = [4]int32{7654321, 17, -8765432, -19}
	input := slices.Clone(e.fixedRawRes)
	beforeMemory := e.fixedHPMem

	e.preprocessFixedInputRes(2)
	if !slices.Equal(e.fixedFiltered, input) {
		t.Fatalf("QEXT fixed input=%v want direct copy %v", e.fixedFiltered, input)
	}
	if e.fixedHPMem != beforeMemory {
		t.Fatalf("QEXT fixed high-pass memory advanced: got=%v want=%v", e.fixedHPMem, beforeMemory)
	}
	if allocs := testing.AllocsPerRun(100, func() { e.preprocessFixedInputRes(2) }); allocs != 0 {
		t.Fatalf("warmed QEXT fixed input copy allocated %g times", allocs)
	}

	e.SetQEXT(false)
	wantFiltered := make([]int32, len(input))
	wantMemory := beforeMemory
	fixedDCRejectRes(input, wantFiltered, &wantMemory, 48000, 2, 3)
	e.preprocessFixedInputRes(2)
	if !slices.Equal(e.fixedFiltered, wantFiltered) || e.fixedHPMem != wantMemory {
		t.Fatalf("QEXT-off fixed input differs from dc_reject: samples=%v/%v memory=%v/%v",
			e.fixedFiltered, wantFiltered, e.fixedHPMem, wantMemory)
	}
}
