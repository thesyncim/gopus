//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestPitchDNNMatchesLibopusOnRealModel(t *testing.T) {
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Skipf("pitchdnn model blob helper unavailable: %v", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone(real pitchdnn blob) error: %v", err)
	}

	var pitch PitchDNN
	if err := pitch.SetModel(blob); err != nil {
		t.Fatalf("PitchDNN.SetModel(real model) error: %v", err)
	}

	var if1 [pitchIFFeatures]float32
	var xcorr1 [pitchXcorrFeatures]float32
	var if2 [pitchIFFeatures]float32
	var xcorr2 [pitchXcorrFeatures]float32
	for i := range if1 {
		if1[i] = float32((i%29)-14) / 17
		if2[i] = float32((i%31)-15) / 13
	}
	for i := range xcorr1 {
		xcorr1[i] = float32((i%37)-18) / 23
		xcorr2[i] = float32((i%41)-20) / 19
	}

	wantPitch1, wantState1, err := probeLibopusPitchDNN(if1[:], xcorr1[:], pitchDNNState{})
	if err != nil {
		t.Skipf("pitchdnn helper unavailable: %v", err)
	}
	gotPitch1 := pitch.Compute(if1[:], xcorr1[:])
	assertFloat32Close(t, []float32{gotPitch1}, []float32{wantPitch1}, 1e-3, "pitch value 1")
	assertFloat32Close(t, pitch.state.gruState[:], wantState1.gruState[:], 5e-3, "pitch gru state 1")
	assertFloat32Close(t, pitch.state.xcorrMem1[:], wantState1.xcorrMem1[:], 5e-3, "pitch xcorr_mem1 1")
	assertFloat32Close(t, pitch.state.xcorrMem2[:], wantState1.xcorrMem2[:], 5e-3, "pitch xcorr_mem2 1")
	assertFloat32Close(t, pitch.state.xcorrMem3[:], wantState1.xcorrMem3[:], 5e-3, "pitch xcorr_mem3 1")

	wantPitch2, wantState2, err := probeLibopusPitchDNN(if2[:], xcorr2[:], wantState1)
	if err != nil {
		t.Fatalf("probeLibopusPitchDNN(second) error: %v", err)
	}
	gotPitch2 := pitch.Compute(if2[:], xcorr2[:])
	assertFloat32Close(t, []float32{gotPitch2}, []float32{wantPitch2}, 1e-3, "pitch value 2")
	assertFloat32Close(t, pitch.state.gruState[:], wantState2.gruState[:], 5e-2, "pitch gru state 2")
	assertFloat32Close(t, pitch.state.xcorrMem1[:], wantState2.xcorrMem1[:], 5e-2, "pitch xcorr_mem1 2")
	assertFloat32Close(t, pitch.state.xcorrMem2[:], wantState2.xcorrMem2[:], 5e-2, "pitch xcorr_mem2 2")
	assertFloat32Close(t, pitch.state.xcorrMem3[:], wantState2.xcorrMem3[:], 5e-2, "pitch xcorr_mem3 2")
}
