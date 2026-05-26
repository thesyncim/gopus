//go:build amd64 && !purego

package celt

import "testing"

func TestPitchFMADD32AVXFMAInstruction(t *testing.T) {
	if !libopusFloatPitchXCorrUsesAVX2FMA() {
		t.Skip("AVX2/FMA unavailable")
	}
	if got := pitchFMADD32(2, 3, 4); got != 10 {
		t.Fatalf("pitchFMADD32(2,3,4)=%v, want 10", got)
	}
	if got := pitchFMADD32(-2, 3, 4); got != -2 {
		t.Fatalf("pitchFMADD32(-2,3,4)=%v, want -2", got)
	}
}
