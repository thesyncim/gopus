package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestSetModelPreservingStateKeepsRuntimeState(t *testing.T) {
	pitchBlob := makePitchDNNTestBlob(t)
	var analysis Analysis
	if err := analysis.SetModel(pitchBlob); err != nil {
		t.Fatalf("Analysis.SetModel error: %v", err)
	}
	analysis.memPreemph = 0.25
	analysis.pitch.state.gruState[0] = 0.5
	analysis.pitch.state.xcorrMem1[0] = -0.25
	beforeMem := analysis.memPreemph
	beforePitch := analysis.pitch.state
	if err := analysis.SetModelPreservingState(pitchBlob); err != nil {
		t.Fatalf("Analysis.SetModelPreservingState error: %v", err)
	}
	if !analysis.Loaded() || analysis.memPreemph != beforeMem || analysis.pitch.state != beforePitch {
		t.Fatalf("analysis reload reset state: loaded=%v mem=%v pitch=%+v", analysis.Loaded(), analysis.memPreemph, analysis.pitch.state)
	}

	predictorBlob, err := dnnblob.Clone(makePredictorTestBlob())
	if err != nil {
		t.Fatalf("Clone(predictor) error: %v", err)
	}
	predictor := newPredictorForTest(t)
	predictor.state.gru1[0] = 0.125
	predictor.state.gru2[0] = -0.375
	beforePredictor := predictor.state
	if err := predictor.SetModelPreservingState(predictorBlob); err != nil {
		t.Fatalf("Predictor.SetModelPreservingState error: %v", err)
	}
	if !predictor.Loaded() || predictor.state != beforePredictor {
		t.Fatalf("predictor reload reset state: loaded=%v state=%+v", predictor.Loaded(), predictor.state)
	}

	farganBlob, err := dnnblob.Clone(makeFARGANTestBlob())
	if err != nil {
		t.Fatalf("Clone(fargan) error: %v", err)
	}
	fargan := newFARGANForTest(t)
	fargan.state.contInitialized = true
	fargan.state.deemphMem = 0.625
	fargan.state.pitchBuf[0] = -0.125
	fargan.state.condConv1State[0] = 0.75
	fargan.state.lastPeriod = 48
	beforeFARGAN := fargan.state
	if err := fargan.SetModelPreservingState(farganBlob); err != nil {
		t.Fatalf("FARGAN.SetModelPreservingState error: %v", err)
	}
	if !fargan.Loaded() || fargan.state != beforeFARGAN {
		t.Fatalf("fargan reload reset state: loaded=%v state=%+v", fargan.Loaded(), fargan.state)
	}
}
