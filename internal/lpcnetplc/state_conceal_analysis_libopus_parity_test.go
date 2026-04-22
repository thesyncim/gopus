//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestConcealFrameFloatWithAnalysisMatchesLibopus(t *testing.T) {
	pitchRaw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Skipf("pitchdnn model blob helper unavailable: %v", err)
	}
	plcRaw, err := probeLibopusPLCModelBlob()
	if err != nil {
		t.Skipf("plc model blob helper unavailable: %v", err)
	}
	farganRaw, err := probeLibopusFARGANModelBlob()
	if err != nil {
		t.Skipf("fargan model blob helper unavailable: %v", err)
	}

	pitchBlob, err := dnnblob.Clone(pitchRaw)
	if err != nil {
		t.Fatalf("Clone(pitch blob) error: %v", err)
	}
	plcBlob, err := dnnblob.Clone(plcRaw)
	if err != nil {
		t.Fatalf("Clone(plc blob) error: %v", err)
	}
	farganBlob, err := dnnblob.Clone(farganRaw)
	if err != nil {
		t.Fatalf("Clone(fargan blob) error: %v", err)
	}

	var state State
	state.Reset()

	history := make([]float32, 4*FrameSize)
	for i := range history {
		history[i] = 0.55*float32(math.Sin(0.015*float64(i))) + 0.12*float32(math.Cos(0.041*float64(i)))
	}
	if got := state.ReplaceHistoryFromFramesFloat(history); got != len(history) {
		t.Fatalf("ReplaceHistoryFromFramesFloat()=%d want %d", got, len(history))
	}

	var queueAnalysis Analysis
	if err := queueAnalysis.SetModel(pitchBlob); err != nil {
		t.Fatalf("queue Analysis.SetModel error: %v", err)
	}
	state.FECClear()
	state.FECAdd(nil)
	for q := 0; q < 3; q++ {
		frame := make([]float32, FrameSize)
		for i := range frame {
			frame[i] = 0.35*float32(math.Sin(0.023*float64(i+q*FrameSize))) + 0.08*float32(math.Cos(0.051*float64(i+17*q)))
		}
		var features [NumTotalFeatures]float32
		if n := queueAnalysis.ComputeSingleFrameFeaturesFloat(features[:], frame); n != NumTotalFeatures {
			t.Fatalf("queue features frame %d ComputeSingleFrameFeaturesFloat()=%d want %d", q, n, NumTotalFeatures)
		}
		state.FECAdd(features[:NumFeatures])
	}

	var analysis Analysis
	if err := analysis.SetModel(pitchBlob); err != nil {
		t.Fatalf("Analysis.SetModel error: %v", err)
	}
	var predictor Predictor
	if err := predictor.SetModel(plcBlob); err != nil {
		t.Fatalf("Predictor.SetModel error: %v", err)
	}
	var fargan FARGAN
	if err := fargan.SetModel(farganBlob); err != nil {
		t.Fatalf("FARGAN.SetModel error: %v", err)
	}

	want, err := probeLibopusPLCConcealWithAnalysis(&state, &fargan, &analysis)
	if err != nil {
		t.Skipf("libopus conceal-analysis helper unavailable: %v", err)
	}

	var frame [FrameSize]float32
	gotFEC := state.GenerateConcealedFrameFloatWithAnalysis(&analysis, &predictor, &fargan, frame[:])
	if gotFEC != want.GotFEC {
		t.Fatalf("GenerateConcealedFrameFloatWithAnalysis gotFEC=%v want %v", gotFEC, want.GotFEC)
	}

	assertFloat32Close(t, frame[:], want.Frame[:], 5e-3, "conceal-analysis frame")
	assertConcealAnalysisStateMatches(t, state, want.State, 5e-3, "conceal-analysis state")
	assertFARGANStateClose(t, fargan.state, libopusFARGANRuntimeResult{
		ContInitialized: want.FARGAN.ContInitialized,
		LastPeriod:      want.FARGAN.LastPeriod,
		DeemphMem:       want.FARGAN.DeemphMem,
		PitchBuf:        want.FARGAN.PitchBuf[:],
		CondConv1State:  want.FARGAN.CondConv1State[:],
		FWC0Mem:         want.FARGAN.FWC0Mem[:],
		GRU1State:       want.FARGAN.GRU1State[:],
		GRU2State:       want.FARGAN.GRU2State[:],
		GRU3State:       want.FARGAN.GRU3State[:],
	}, 5e-3, "conceal-analysis fargan")
	assertAnalysisMatches(t, &analysis, want.Analysis, "conceal-analysis analysis")
}

func assertConcealAnalysisStateMatches(t *testing.T, got State, want StateSnapshot, tol float64, label string) {
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
	if got.fecReadPos != want.FECReadPos {
		t.Fatalf("%s fecReadPos=%d want %d", label, got.fecReadPos, want.FECReadPos)
	}
	if got.fecFillPos != want.FECFillPos {
		t.Fatalf("%s fecFillPos=%d want %d", label, got.fecFillPos, want.FECFillPos)
	}
	if got.fecSkip != want.FECSkip {
		t.Fatalf("%s fecSkip=%d want %d", label, got.fecSkip, want.FECSkip)
	}
	assertFloat32Close(t, got.features[:], want.Features[:], tol, label+" features")
	assertFloat32Close(t, got.cont[:], want.Cont[:], tol, label+" cont")
	assertFloat32Close(t, got.pcm[:], want.PCM[:], tol, label+" pcm")
	assertFloat32Close(t, got.plcNet.gru1[:], want.PLCNet.GRU1[:], tol, label+" plc net gru1")
	assertFloat32Close(t, got.plcNet.gru2[:], want.PLCNet.GRU2[:], tol, label+" plc net gru2")
	assertFloat32Close(t, got.plcBak[0].gru1[:], want.PLCBak[0].GRU1[:], tol, label+" plc bak0 gru1")
	assertFloat32Close(t, got.plcBak[0].gru2[:], want.PLCBak[0].GRU2[:], tol, label+" plc bak0 gru2")
	assertFloat32Close(t, got.plcBak[1].gru1[:], want.PLCBak[1].GRU1[:], tol, label+" plc bak1 gru1")
	assertFloat32Close(t, got.plcBak[1].gru2[:], want.PLCBak[1].GRU2[:], tol, label+" plc bak1 gru2")
}
