//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestBoundedConcealFrameFloatMatchesLibopus(t *testing.T) {
	plcModelBlob, err := probeLibopusPLCModelBlob()
	if err != nil {
		t.Skipf("libopus plc model helper unavailable: %v", err)
	}
	plcBlob, err := dnnblob.Clone(plcModelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(plc) error: %v", err)
	}
	farganModelBlob, err := probeLibopusFARGANModelBlob()
	if err != nil {
		t.Skipf("libopus fargan model helper unavailable: %v", err)
	}
	farganBlob, err := dnnblob.Clone(farganModelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(fargan) error: %v", err)
	}
	var predictor Predictor
	if err := predictor.SetModel(plcBlob); err != nil {
		t.Fatalf("Predictor.SetModel(real model) error: %v", err)
	}
	var fargan FARGAN
	if err := fargan.SetModel(farganBlob); err != nil {
		t.Fatalf("FARGAN.SetModel(real model) error: %v", err)
	}

	var st State
	seedPredictorBackupsForTest(&predictor, &st)
	fec0, fec1 := seedBoundedConcealStateForTest(&st)
	predictor.setState(&st.plcNet)

	want1, err := probeLibopusPLCConceal(st, fargan.state, fec0[:], fec1[:])
	if err != nil {
		t.Skipf("libopus plc conceal helper unavailable: %v", err)
	}

	var frame1 [FrameSize]float32
	gotFEC1 := st.ConcealFrameFloat(&predictor, &fargan, frame1[:])
	if gotFEC1 != want1.GotFEC {
		t.Fatalf("first conceal gotFEC=%v want %v", gotFEC1, want1.GotFEC)
	}
	assertBoundedConcealMatchesLibopus(t, st, fargan.state, frame1[:], want1, 1.2e-1, 1.2e-1, "first conceal")

	state2 := stateFromLibopusConcealResult(want1, 2)
	fargan2 := farganStateFromLibopusResult(want1.FARGAN)
	want2, err := probeLibopusPLCConceal(state2, fargan2, fec0[:], fec1[:])
	if err != nil {
		t.Skipf("libopus plc conceal helper unavailable on second step: %v", err)
	}

	var predictor2 Predictor
	predictor2 = predictor
	predictor2.setState(&state2.plcNet)
	var farganGo2 FARGAN
	farganGo2 = fargan
	farganGo2.state = fargan2
	var frame2 [FrameSize]float32
	gotFEC2 := state2.ConcealFrameFloat(&predictor2, &farganGo2, frame2[:])
	if gotFEC2 != want2.GotFEC {
		t.Fatalf("second conceal gotFEC=%v want %v", gotFEC2, want2.GotFEC)
	}
	assertBoundedConcealMatchesLibopus(t, state2, farganGo2.state, frame2[:], want2, 1.2e-1, 1.4e-1, "second conceal")
}

func TestConcealFrameFloatWithAnalysisMatchesLibopusColdStart(t *testing.T) {
	plcModelBlob, err := probeLibopusPLCModelBlob()
	if err != nil {
		t.Skipf("libopus plc model helper unavailable: %v", err)
	}
	plcBlob, err := dnnblob.Clone(plcModelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(plc) error: %v", err)
	}
	farganModelBlob, err := probeLibopusFARGANModelBlob()
	if err != nil {
		t.Skipf("libopus fargan model helper unavailable: %v", err)
	}
	farganBlob, err := dnnblob.Clone(farganModelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(fargan) error: %v", err)
	}
	pitchModelBlob, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Skipf("libopus pitchdnn model helper unavailable: %v", err)
	}
	pitchBlob, err := dnnblob.Clone(pitchModelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone(pitchdnn) error: %v", err)
	}
	var predictor Predictor
	if err := predictor.SetModel(plcBlob); err != nil {
		t.Fatalf("Predictor.SetModel(real model) error: %v", err)
	}
	var fargan FARGAN
	if err := fargan.SetModel(farganBlob); err != nil {
		t.Fatalf("FARGAN.SetModel(real model) error: %v", err)
	}
	var analysis Analysis
	if err := analysis.SetModel(pitchBlob); err != nil {
		t.Fatalf("Analysis.SetModel(real model) error: %v", err)
	}

	var st State
	seedPredictorBackupsForTest(&predictor, &st)
	fec0, fec1 := seedBoundedConcealStateForTest(&st)

	want, err := probeLibopusPLCConceal(st, fargan.state, fec0[:], fec1[:])
	if err != nil {
		t.Skipf("libopus plc conceal helper unavailable: %v", err)
	}

	var frame [FrameSize]float32
	gotFEC := st.ConcealFrameFloatWithAnalysis(&analysis, &predictor, &fargan, frame[:])
	if gotFEC != want.GotFEC {
		t.Fatalf("conceal gotFEC=%v want %v", gotFEC, want.GotFEC)
	}
	assertBoundedConcealMatchesLibopus(t, st, fargan.state, frame[:], want, 1.2e-1, 1.2e-1, "conceal with analysis")
}

func stateFromLibopusConcealResult(result libopusPLCConcealResult, fecFillPos int) State {
	var st State
	st.runtimeInit = true
	st.blend = result.Blend
	st.lossCount = result.LossCount
	st.analysisGap = result.AnalysisGap
	st.analysisPos = result.AnalysisPos
	st.predictPos = result.PredictPos
	st.fecReadPos = result.FECRead
	st.fecFillPos = fecFillPos
	st.fecSkip = result.FECSkip
	copy(st.features[:], result.Features)
	copy(st.cont[:], result.Cont)
	copy(st.pcm[:], result.PCM)
	copy(st.plcNet.gru1[:], result.PLCNet.gru1[:])
	copy(st.plcNet.gru2[:], result.PLCNet.gru2[:])
	copy(st.plcBak[0].gru1[:], result.PLCBak[0].gru1[:])
	copy(st.plcBak[0].gru2[:], result.PLCBak[0].gru2[:])
	copy(st.plcBak[1].gru1[:], result.PLCBak[1].gru1[:])
	copy(st.plcBak[1].gru2[:], result.PLCBak[1].gru2[:])
	return st
}

func assertBoundedConcealMatchesLibopus(t *testing.T, got State, gotFARGAN FARGANState, gotFrame []float32, want libopusPLCConcealResult, tol, farganTol float64, label string) {
	t.Helper()
	if got.blend != want.Blend {
		t.Fatalf("%s blend=%d want %d", label, got.blend, want.Blend)
	}
	if got.lossCount != want.LossCount {
		t.Fatalf("%s lossCount=%d want %d", label, got.lossCount, want.LossCount)
	}
	if got.analysisGap != want.AnalysisGap {
		t.Fatalf("%s analysisGap=%d want %d", label, got.analysisGap, want.AnalysisGap)
	}
	if got.analysisPos != want.AnalysisPos {
		t.Fatalf("%s analysisPos=%d want %d", label, got.analysisPos, want.AnalysisPos)
	}
	if got.predictPos != want.PredictPos {
		t.Fatalf("%s predictPos=%d want %d", label, got.predictPos, want.PredictPos)
	}
	if got.fecReadPos != want.FECRead {
		t.Fatalf("%s fecReadPos=%d want %d", label, got.fecReadPos, want.FECRead)
	}
	if got.fecSkip != want.FECSkip {
		t.Fatalf("%s fecSkip=%d want %d", label, got.fecSkip, want.FECSkip)
	}
	assertFloat32Close(t, gotFrame, want.Frame, tol, label+" frame")
	assertFloat32Close(t, got.features[:], want.Features, tol, label+" features")
	assertFloat32Close(t, got.cont[:], want.Cont, tol, label+" cont")
	assertFloat32Close(t, got.pcm[:], want.PCM, tol, label+" pcm")
	assertFloat32Close(t, got.plcNet.gru1[:], want.PLCNet.gru1[:], tol, label+" plc net gru1")
	assertFloat32Close(t, got.plcNet.gru2[:], want.PLCNet.gru2[:], tol, label+" plc net gru2")
	assertFloat32Close(t, got.plcBak[0].gru1[:], want.PLCBak[0].gru1[:], tol, label+" plc bak0 gru1")
	assertFloat32Close(t, got.plcBak[0].gru2[:], want.PLCBak[0].gru2[:], tol, label+" plc bak0 gru2")
	assertFloat32Close(t, got.plcBak[1].gru1[:], want.PLCBak[1].gru1[:], tol, label+" plc bak1 gru1")
	assertFloat32Close(t, got.plcBak[1].gru2[:], want.PLCBak[1].gru2[:], tol, label+" plc bak1 gru2")
	assertFARGANStateClose(t, gotFARGAN, want.FARGAN, farganTol, label+" fargan")
}
