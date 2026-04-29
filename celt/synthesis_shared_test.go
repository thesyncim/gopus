package celt

import "testing"

func TestSynthesizeStereoPlanarFromMonoLongMatchesDuplicatedStereo(t *testing.T) {
	for _, frameSize := range []int{120, 240, 480, 960} {
		coeffs := make([]float64, frameSize)
		for i := range coeffs {
			coeffs[i] = float64((i%17)-8) * 0.125
		}

		legacy := NewDecoder(2)
		shared := NewDecoder(2)
		for i := 0; i < Overlap*2; i++ {
			v := float64((i%23)-11) * 0.03125
			legacy.overlapBuffer[i] = v
			shared.overlapBuffer[i] = v
		}

		coeffsR := make([]float64, len(coeffs))
		copy(coeffsR, coeffs)
		wantL, wantR := legacy.synthesizeStereoPlanar(coeffs, coeffsR, false, 1)
		gotL, gotR := shared.synthesizeStereoPlanarFromMonoLong(coeffs)

		if len(gotL) != len(wantL) || len(gotR) != len(wantR) {
			t.Fatalf("frameSize=%d lengths got=(%d,%d) want=(%d,%d)", frameSize, len(gotL), len(gotR), len(wantL), len(wantR))
		}
		for i := range wantL {
			if gotL[i] != wantL[i] {
				t.Fatalf("frameSize=%d left[%d] got %.17g want %.17g", frameSize, i, gotL[i], wantL[i])
			}
			if gotR[i] != wantR[i] {
				t.Fatalf("frameSize=%d right[%d] got %.17g want %.17g", frameSize, i, gotR[i], wantR[i])
			}
		}
		for i := range legacy.overlapBuffer {
			if shared.overlapBuffer[i] != legacy.overlapBuffer[i] {
				t.Fatalf("frameSize=%d overlap[%d] got %.17g want %.17g", frameSize, i, shared.overlapBuffer[i], legacy.overlapBuffer[i])
			}
		}
	}
}
