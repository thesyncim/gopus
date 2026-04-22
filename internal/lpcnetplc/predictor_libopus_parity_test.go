//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestPredictorMatchesLibopusOnRealModel(t *testing.T) {
	modelBlob, err := probeLibopusPLCModelBlob()
	if err != nil {
		t.Skipf("libopus plc model helper unavailable: %v", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	var predictor Predictor
	if err := predictor.SetModel(blob); err != nil {
		t.Fatalf("Predictor.SetModel(real model) error: %v", err)
	}

	var input1 [InputSize]float32
	var input2 [InputSize]float32
	for i := 0; i < NumFeatures; i++ {
		input2[2*NumBands+i] = float32((i%7)-3) / 11
	}
	input2[2*NumBands+NumFeatures] = -1

	var out [NumFeatures]float32
	var zeroGRU1 [GRU1Size]float32
	var zeroGRU2 [GRU2Size]float32

	want1, wantGRU1, wantGRU2, err := probeLibopusPLCPredict(input1[:], zeroGRU1[:], zeroGRU2[:])
	if err != nil {
		t.Skipf("libopus plc predict helper unavailable: %v", err)
	}
	if n := predictor.Predict(out[:], input1[:]); n != NumFeatures {
		t.Fatalf("Predict(input1)=%d want %d", n, NumFeatures)
	}
	assertFloat32Close(t, out[:], want1, 5e-3, "predict output 1")
	assertFloat32Close(t, predictor.state.gru1[:], wantGRU1, 5e-3, "gru1 state after input1")
	assertFloat32Close(t, predictor.state.gru2[:], wantGRU2, 5e-3, "gru2 state after input1")

	want2, wantGRU1b, wantGRU2b, err := probeLibopusPLCPredict(input2[:], wantGRU1, wantGRU2)
	if err != nil {
		t.Skipf("libopus plc predict helper unavailable on second step: %v", err)
	}
	if n := predictor.Predict(out[:], input2[:]); n != NumFeatures {
		t.Fatalf("Predict(input2)=%d want %d", n, NumFeatures)
	}
	assertFloat32Close(t, out[:], want2, 5e-2, "predict output 2")
	assertFloat32Close(t, predictor.state.gru1[:], wantGRU1b, 5e-2, "gru1 state after input2")
	assertFloat32Close(t, predictor.state.gru2[:], wantGRU2b, 5e-2, "gru2 state after input2")
}

func assertFloat32Close(t *testing.T, got, want []float32, tol float64, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Abs(float64(got[i]-want[i])) > tol {
			t.Fatalf("%s[%d]=%v want %v (tol=%g)", label, i, got[i], want[i], tol)
		}
	}
}
