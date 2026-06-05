package celt

import (
	"math"
	"testing"
)

func TestSynthesizeStereoPlanarFromMonoLongMatchesDuplicatedStereo(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		coeffs := make([]float32, frameSize)
		for i := range coeffs {
			coeffs[i] = float32((i%17)-8) * 0.125
		}

		legacy := NewDecoder(2)
		shared := NewDecoder(2)
		for i := range Overlap * 2 {
			v := float64((i%23)-11) * 0.03125
			legacy.overlapBuffer[i] = celtSig(v)
			shared.overlapBuffer[i] = celtSig(v)
		}

		coeffsR := make([]float32, len(coeffs))
		copy(coeffsR, coeffs)
		wantL, wantR := legacy.synthesizeStereoPlanar(coeffs, coeffsR, false, 1)
		gotL, gotR := shared.synthesizeStereoPlanarFromMonoLong(coeffs)

		if len(gotL) != len(wantL) || len(gotR) != len(wantR) {
			t.Fatalf("frameSize=%d lengths got=(%d,%d) want=(%d,%d)", frameSize, len(gotL), len(gotR), len(wantL), len(wantR))
		}
		for i := range wantL {
			if !nearlyEqualSynthesis(gotL[i], wantL[i]) {
				t.Fatalf("frameSize=%d left[%d] got %.17g want %.17g", frameSize, i, gotL[i], wantL[i])
			}
			if !nearlyEqualSynthesis(gotR[i], wantR[i]) {
				t.Fatalf("frameSize=%d right[%d] got %.17g want %.17g", frameSize, i, gotR[i], wantR[i])
			}
		}
		for i := range legacy.overlapBuffer {
			if !nearlyEqualSynthesis(float32(shared.overlapBuffer[i]), float32(legacy.overlapBuffer[i])) {
				t.Fatalf("frameSize=%d overlap[%d] got %.17g want %.17g", frameSize, i, shared.overlapBuffer[i], legacy.overlapBuffer[i])
			}
		}
	}
}

func nearlyEqualSynthesis(got, want float32) bool {
	if got == want {
		return true
	}
	gotBits := math.Float32bits(got)
	wantBits := math.Float32bits(want)
	if (gotBits >> 31) != (wantBits >> 31) {
		return false
	}
	if gotBits > wantBits {
		gotBits, wantBits = wantBits, gotBits
	}
	return wantBits-gotBits <= 1
}
