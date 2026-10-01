//go:build gopus_osce

package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
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
	assertFARGANStateMatchesLibopus(t, runtime.state, want, "prime continuity")
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
	assertFloat32BitsMatch(t, out[:], wantSynth.PCM, "synthesize pcm")
	assertFARGANStateMatchesLibopus(t, runtime.state, wantSynth, "synthesize state")
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

func assertFARGANStateMatchesLibopus(t *testing.T, got FARGANState, want libopusFARGANRuntimeResult, label string) {
	t.Helper()
	if got.contInitialized != want.ContInitialized {
		t.Fatalf("%s contInitialized=%v want %v", label, got.contInitialized, want.ContInitialized)
	}
	if got.lastPeriod != want.LastPeriod {
		t.Fatalf("%s lastPeriod=%d want %d", label, got.lastPeriod, want.LastPeriod)
	}
	assertFloat32BitsMatch(t, []float32{got.deemphMem}, []float32{want.DeemphMem}, label+" deemph")
	assertFloat32BitsMatch(t, got.pitchBuf[:], want.PitchBuf, label+" pitch_buf")
	assertFloat32BitsMatch(t, got.condConv1State[:], want.CondConv1State, label+" cond_conv1_state")
	assertFloat32BitsMatch(t, got.fwc0Mem[:], want.FWC0Mem, label+" fwc0_mem")
	assertFloat32BitsMatch(t, got.gru1State[:], want.GRU1State, label+" gru1_state")
	assertFloat32BitsMatch(t, got.gru2State[:], want.GRU2State, label+" gru2_state")
	assertFloat32BitsMatch(t, got.gru3State[:], want.GRU3State, label+" gru3_state")
}
