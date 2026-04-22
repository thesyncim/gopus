//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package lpcnetplc

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

func TestLPCNetSingleFrameFeaturesFloatMatchesLibopusColdStart(t *testing.T) {
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Skipf("pitchdnn model blob helper unavailable: %v", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone(real pitchdnn blob) error: %v", err)
	}

	var analysis Analysis
	if err := analysis.SetModel(blob); err != nil {
		t.Fatalf("Analysis.SetModel(real model) error: %v", err)
	}

	var frame [FrameSize]float32
	for i := range frame {
		frame[i] = float32((i%43)-21) / 17
	}
	want, err := probeLibopusLPCNetFeatures(frame[:])
	if err != nil {
		t.Skipf("lpcnet features helper unavailable: %v", err)
	}

	var got [NumTotalFeatures]float32
	if n := analysis.ComputeSingleFrameFeaturesFloat(got[:], frame[:]); n != NumTotalFeatures {
		t.Fatalf("ComputeSingleFrameFeaturesFloat()=%d want %d", n, NumTotalFeatures)
	}
	assertFloat32Close(t, got[:], want.Features[:NumTotalFeatures], 5e-3, "cold start features copy-out")
	assertAnalysisMatches(t, &analysis, want, "cold start")
}

func TestLPCNetSingleFrameFeaturesFloatMatchesLibopusStatefulSequence(t *testing.T) {
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Skipf("pitchdnn model blob helper unavailable: %v", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("Clone(real pitchdnn blob) error: %v", err)
	}

	var analysis Analysis
	if err := analysis.SetModel(blob); err != nil {
		t.Fatalf("Analysis.SetModel(real model) error: %v", err)
	}

	frames := make([]float32, 3*FrameSize)
	for i := range frames {
		frames[i] = float32((i%53)-26) / 19
	}
	want, err := probeLibopusLPCNetFeatures(frames)
	if err != nil {
		t.Skipf("lpcnet features helper unavailable: %v", err)
	}
	for i := 0; i < len(frames); i += FrameSize {
		var got [NumTotalFeatures]float32
		if n := analysis.ComputeSingleFrameFeaturesFloat(got[:], frames[i:i+FrameSize]); n != NumTotalFeatures {
			t.Fatalf("frame %d ComputeSingleFrameFeaturesFloat()=%d want %d", i/FrameSize, n, NumTotalFeatures)
		}
		base := (i / FrameSize) * NumTotalFeatures
		assertFloat32Close(t, got[:], want.Features[base:base+NumTotalFeatures], 5e-2, "sequence features")
	}
	assertAnalysisMatches(t, &analysis, want, "stateful sequence")
}

func assertAnalysisMatches(t *testing.T, got *Analysis, want libopusLPCNetFeaturesResult, label string) {
	t.Helper()
	assertFloat32Close(t, got.analysisMem[:], want.AnalysisMem, 5e-3, label+" analysis_mem")
	assertFloat32Close(t, []float32{got.memPreemph}, []float32{want.MemPreemph}, 5e-3, label+" mem_preemph")
	assertComplex64Close(t, got.prevIF[:], want.PrevIF, 5e-3, label+" prev_if")
	assertFloat32Close(t, got.ifFeatures[:], want.IFFeatures, 5e-3, label+" if_features")
	assertFloat32Close(t, got.lpc[:], want.LPC, 5e-2, label+" lpc")
	assertFloat32Close(t, got.pitchMem[:], want.PitchMem, 5e-3, label+" pitch_mem")
	assertFloat32Close(t, []float32{got.pitchFilt}, []float32{want.PitchFilt}, 5e-3, label+" pitch_filt")
	assertFloat32Close(t, got.excBuf[:], want.ExcBuf, 5e-2, label+" exc_buf")
	assertFloat32Close(t, got.lpBuf[:], want.LPBuf, 5e-2, label+" lp_buf")
	assertFloat32Close(t, got.lpMem[:], want.LPMem, 5e-2, label+" lp_mem")
	assertFloat32Close(t, got.xcorrFeatures[:], want.XCorr, 5e-2, label+" xcorr_features")
	assertFloat32Close(t, []float32{got.dnnPitch}, []float32{want.DNNPitch}, 5e-3, label+" dnn_pitch")
	assertFloat32Close(t, got.pitch.state.gruState[:], want.PitchState.gruState[:], 5e-2, label+" pitch gru_state")
	assertFloat32Close(t, got.pitch.state.xcorrMem1[:], want.PitchState.xcorrMem1[:], 5e-2, label+" pitch xcorr_mem1")
	assertFloat32Close(t, got.pitch.state.xcorrMem2[:], want.PitchState.xcorrMem2[:], 5e-2, label+" pitch xcorr_mem2")
	assertFloat32Close(t, got.pitch.state.xcorrMem3[:], want.PitchState.xcorrMem3[:], 5e-2, label+" pitch xcorr_mem3")
}

func assertComplex64Close(t *testing.T, got, want []complex64, tol float64, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		assertFloat32Close(t, []float32{real(got[i]), imag(got[i])}, []float32{real(want[i]), imag(want[i])}, tol, label)
	}
}
