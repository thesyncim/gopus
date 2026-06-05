//go:build gopus_osce

package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestPredictorBitExactMatchesLibopus checks the PLC feature predictor (the
// recurrent compute_generic_gru path) against the libopus oracle bit-exactly to
// guard the GRU fused multiply-add operand-order parity.
func TestPredictorBitExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusPLCModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "plc model", err)
	}
	blob, _ := dnnblob.Clone(modelBlob)
	var predictor Predictor
	if err := predictor.SetModel(blob); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	var input [InputSize]float32
	for i := 0; i < NumFeatures; i++ {
		input[2*NumBands+i] = float32((i%7)-3) / 11
	}
	input[2*NumBands+NumFeatures] = -1
	var z1 [GRU1Size]float32
	var z2 [GRU2Size]float32
	for i := range z1 {
		z1[i] = float32((i%13)-6) / 17
	}
	for i := range z2 {
		z2[i] = float32((i%11)-5) / 19
	}
	predictor.state.gru1 = z1
	predictor.state.gru2 = z2
	want, wantG1, wantG2, err := probeLibopusPLCPredict(input[:], z1[:], z2[:])
	if err != nil {
		libopustest.HelperUnavailable(t, "plc predict", err)
	}
	var out [NumFeatures]float32
	predictor.Predict(out[:], input[:])
	rep := func(label string, g, w []float32) {
		for i := range g {
			if math.Float32bits(g[i]) != math.Float32bits(w[i]) {
				t.Errorf("%s[%d] got=0x%08x want=0x%08x", label, i, math.Float32bits(g[i]), math.Float32bits(w[i]))
				return
			}
		}
	}
	rep("out", out[:], want)
	rep("gru1", predictor.state.gru1[:], wantG1)
	rep("gru2", predictor.state.gru2[:], wantG2)
}
