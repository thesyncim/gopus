//go:build gopus_osce

package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPredictorMatchesLibopusOnRealModel(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusPLCModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "plc model", err)
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
		libopustest.HelperUnavailable(t, "plc predict", err)
	}
	if n := predictor.Predict(out[:], input1[:]); n != NumFeatures {
		t.Fatalf("Predict(input1)=%d want %d", n, NumFeatures)
	}
	assertFloat32BitsMatch(t, out[:], want1, "predict output 1")
	assertFloat32BitsMatch(t, predictor.state.gru1[:], wantGRU1, "gru1 state after input1")
	assertFloat32BitsMatch(t, predictor.state.gru2[:], wantGRU2, "gru2 state after input1")

	want2, wantGRU1b, wantGRU2b, err := probeLibopusPLCPredict(input2[:], wantGRU1, wantGRU2)
	if err != nil {
		libopustest.HelperUnavailable(t, "plc predict second step", err)
	}
	if n := predictor.Predict(out[:], input2[:]); n != NumFeatures {
		t.Fatalf("Predict(input2)=%d want %d", n, NumFeatures)
	}
	assertFloat32BitsMatch(t, out[:], want2, "predict output 2")
	assertFloat32BitsMatch(t, predictor.state.gru1[:], wantGRU1b, "gru1 state after input2")
	assertFloat32BitsMatch(t, predictor.state.gru2[:], wantGRU2b, "gru2 state after input2")
}

// assertFloat32BitsMatch compares float32 outputs and state against the
// selected libopus helper without a numeric tolerance.
func assertFloat32BitsMatch(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("%s[%d] Go=%08x C=%08x", label, i, gotBits, wantBits)
		}
	}
}
