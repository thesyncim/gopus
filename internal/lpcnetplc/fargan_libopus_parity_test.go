//go:build gopus_extra_controls
// +build gopus_extra_controls

package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	// Retained recurrent FARGAN state varies slightly across libopus DNN
	// backends; keep synthesized PCM tight and pin private state to the same
	// recurrent-state band used by the neighboring predictor/analysis seams.
	farganPrimeStateLibopusTol = 6e-2
	farganSynthPCMLibopusTol   = 1e-3
	farganSynthStateLibopusTol = 6e-2
)

func TestFARGANPrimeContinuityMatchesLibopusOnRealModel(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusFARGANModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan model", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	var runtime FARGAN
	if err := runtime.SetModel(blob); err != nil {
		t.Fatalf("FARGAN.SetModel(real model) error: %v", err)
	}

	var pcm0 [FARGANContSamples]float32
	var contFeatures [ContVectors * NumFeatures]float32
	fillFARGANPrimeInputs(pcm0[:], contFeatures[:])

	want, err := probeLibopusFARGANContinuity(pcm0[:], contFeatures[:])
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan continuity", err)
	}
	if n := runtime.PrimeContinuity(pcm0[:], contFeatures[:]); n != FARGANContSamples {
		t.Fatalf("PrimeContinuity()=%d want %d", n, FARGANContSamples)
	}
	assertFARGANStateClose(t, runtime.state, want, farganPrimeStateLibopusTol, "prime continuity")
}

func TestFARGANSynthesizeMatchesLibopusOnRealModel(t *testing.T) {
	libopustest.RequireOracle(t)
	modelBlob, err := probeLibopusFARGANModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan model", err)
	}
	blob, err := dnnblob.Clone(modelBlob)
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	var runtime FARGAN
	if err := runtime.SetModel(blob); err != nil {
		t.Fatalf("FARGAN.SetModel(real model) error: %v", err)
	}

	var pcm0 [FARGANContSamples]float32
	var contFeatures [ContVectors * NumFeatures]float32
	var frameFeatures [NumFeatures]float32
	var out [FARGANFrameSize]float32
	fillFARGANPrimeInputs(pcm0[:], contFeatures[:])
	fillFARGANFeatures(frameFeatures[:])

	wantCont, err := probeLibopusFARGANContinuity(pcm0[:], contFeatures[:])
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan continuity", err)
	}
	if n := runtime.PrimeContinuity(pcm0[:], contFeatures[:]); n != FARGANContSamples {
		t.Fatalf("PrimeContinuity()=%d want %d", n, FARGANContSamples)
	}
	wantSynth, err := probeLibopusFARGANSynthesize(farganStateFromLibopusResult(wantCont), frameFeatures[:])
	if err != nil {
		libopustest.HelperUnavailable(t, "fargan synth", err)
	}
	if n := runtime.Synthesize(out[:], frameFeatures[:]); n != FARGANFrameSize {
		t.Fatalf("Synthesize()=%d want %d", n, FARGANFrameSize)
	}
	assertFloat32Close(t, out[:], wantSynth.PCM, farganSynthPCMLibopusTol, "synthesize pcm")
	assertFARGANStateClose(t, runtime.state, wantSynth, farganSynthStateLibopusTol, "synthesize state")
}

func farganStateFromLibopusResult(result libopusFARGANRuntimeResult) FARGANState {
	var state FARGANState
	state.contInitialized = result.ContInitialized
	state.lastPeriod = result.LastPeriod
	state.deemphMem = result.DeemphMem
	copy(state.pitchBuf[:], result.PitchBuf)
	copy(state.condConv1State[:], result.CondConv1State)
	copy(state.fwc0Mem[:], result.FWC0Mem)
	copy(state.gru1State[:], result.GRU1State)
	copy(state.gru2State[:], result.GRU2State)
	copy(state.gru3State[:], result.GRU3State)
	return state
}

func assertFARGANStateClose(t *testing.T, got FARGANState, want libopusFARGANRuntimeResult, tol float64, label string) {
	t.Helper()
	if got.contInitialized != want.ContInitialized {
		t.Fatalf("%s contInitialized=%v want %v", label, got.contInitialized, want.ContInitialized)
	}
	if got.lastPeriod < want.LastPeriod-1 || got.lastPeriod > want.LastPeriod+1 {
		t.Fatalf("%s lastPeriod=%d want %d (+/-1)", label, got.lastPeriod, want.LastPeriod)
	}
	assertFloat32Close(t, []float32{got.deemphMem}, []float32{want.DeemphMem}, tol, label+" deemph")
	assertFloat32Close(t, got.pitchBuf[:], want.PitchBuf, tol, label+" pitch_buf")
	assertFloat32Close(t, got.condConv1State[:], want.CondConv1State, tol, label+" cond_conv1_state")
	assertFloat32Close(t, got.fwc0Mem[:], want.FWC0Mem, tol, label+" fwc0_mem")
	assertFloat32Close(t, got.gru1State[:], want.GRU1State, tol, label+" gru1_state")
	assertFloat32Close(t, got.gru2State[:], want.GRU2State, tol, label+" gru2_state")
	assertFloat32Close(t, got.gru3State[:], want.GRU3State, tol, label+" gru3_state")
}
