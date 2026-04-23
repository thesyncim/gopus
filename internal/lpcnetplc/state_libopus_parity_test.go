//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestPrefillAndConcealmentFeatureStepMatchLibopus(t *testing.T) {
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

	var st State
	var tmpOut [NumFeatures]float32
	var seed1 [InputSize]float32
	var seed2 [InputSize]float32
	for i := 0; i < NumFeatures; i++ {
		seed1[2*NumBands+i] = float32((i%7)-3) / 11
		seed2[2*NumBands+i] = float32((i%5)-2) / 9
	}
	seed1[2*NumBands+NumFeatures] = -1
	seed2[2*NumBands+NumFeatures] = -1
	predictor.Reset()
	predictor.Predict(tmpOut[:], seed1[:])
	predictor.copyState(&st.plcBak[0])
	predictor.Predict(tmpOut[:], seed2[:])
	predictor.copyState(&st.plcBak[1])
	predictor.copyState(&st.plcNet)
	predictor.setState(&st.plcNet)

	var fec0 [NumFeatures]float32
	var fec1 [NumFeatures]float32
	for i := range fec0 {
		fec0[i] = float32(i+1) / 13
		fec1[i] = float32((i%5)+1) / 7
	}
	st.FECAdd(fec0[:])
	st.FECAdd(fec1[:])

	var features [NumTotalFeatures]float32
	var cont [ContVectors * NumFeatures]float32
	copy(features[:], st.features[:])
	copy(cont[:], st.cont[:])
	want, err := probeLibopusPLCPrefill(features[:], cont[:], fec0[:], fec1[:], st.plcNet, st.plcBak, 2, 0, st.lossCount, true, true)
	if err != nil {
		t.Skipf("libopus plc prefill helper unavailable: %v", err)
	}

	if n := st.PrimeFirstLossPrefill(&predictor); n != 2 {
		t.Fatalf("PrimeFirstLossPrefill()=%d want 2", n)
	}
	if gotFEC := st.ConcealmentFeatureStep(&predictor); gotFEC {
		t.Fatal("ConcealmentFeatureStep()=queued FEC want predicted")
	}

	if st.lossCount != want.LossCount {
		t.Fatalf("lossCount=%d want %d", st.lossCount, want.LossCount)
	}
	if st.fecReadPos != want.FECRead {
		t.Fatalf("fecReadPos=%d want %d", st.fecReadPos, want.FECRead)
	}
	if st.fecSkip != want.FECSkip {
		t.Fatalf("fecSkip=%d want %d", st.fecSkip, want.FECSkip)
	}
	assertFloat32Close(t, st.features[:], want.Features, 5e-2, "plc features")
	assertFloat32Close(t, st.cont[:], want.Cont, 5e-2, "plc cont")
	assertFloat32Close(t, st.plcNet.gru1[:], want.PLCNet.gru1[:], 5e-2, "plc net gru1")
	assertFloat32Close(t, st.plcNet.gru2[:], want.PLCNet.gru2[:], 5e-2, "plc net gru2")
	assertFloat32Close(t, st.plcBak[0].gru1[:], want.PLCBak[0].gru1[:], 5e-2, "plc bak0 gru1")
	assertFloat32Close(t, st.plcBak[0].gru2[:], want.PLCBak[0].gru2[:], 5e-2, "plc bak0 gru2")
	assertFloat32Close(t, st.plcBak[1].gru1[:], want.PLCBak[1].gru1[:], 5e-2, "plc bak1 gru1")
	assertFloat32Close(t, st.plcBak[1].gru2[:], want.PLCBak[1].gru2[:], 5e-2, "plc bak1 gru2")
}
