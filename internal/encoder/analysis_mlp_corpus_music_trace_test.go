//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// TestAnalysisMLPCorpusMusicTrace captures the actual frame-zero MLP operands
// for the corpus_music_v1 case in TestAnalysisMatchesLibopusLive. Cross-language
// arithmetic differences are reported as diagnostics; the existing live
// analysis parity test remains the strict gate for this corpus case.
func TestAnalysisMLPCorpusMusicTrace(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis MLP corpus music trace")
	if !analysisMLPTraceEnabled {
		t.Fatal("analysis MLP stage trace hook is disabled in this build")
	}

	const (
		fs        = 48000
		channels  = 1
		frameSize = 960
		frames    = 60 // max(1200/20ms, 12), matching TestAnalysisMatchesLibopusLive.
		lsbDepth  = 24
	)
	samples, err := testsignal.GenerateCorpusSignal(
		testsignal.CorpusMusicV1,
		fs,
		frames*frameSize*channels,
		channels,
	)
	if err != nil {
		t.Fatalf("generate corpus_music_v1 analysis input: %v", err)
	}
	input := analysisMLPGANIInput(fs, channels, frameSize, frames, lsbDepth, samples)

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		t.Fatalf("run baseline tonality analysis helper: %v", err)
	}
	if err := validateAnalysisGANO(baseline, frames); err != nil {
		t.Fatalf("baseline GANO structure: %v", err)
	}

	root := celtQuantTraceRepoRoot(t)
	tracePath, err := libopusAnalysisMLPStageTraceHelper.Path(func() (string, error) {
		return buildLibopusAnalysisMLPStageTraceHelper(root)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis MLP stage trace", err)
	}
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis MLP stage trace", err)
	}
	const infoBytes = 12*4 + 20
	const recordBytes = 2*infoBytes + 12*4 + 12*4
	const baselineHeaderBytes = 12
	baselineBytes := baselineHeaderBytes + frames*recordBytes
	if len(traced) < baselineBytes {
		t.Fatalf("instrumented C output has %d bytes, shorter than GANO prefix %d", len(traced), baselineBytes)
	}
	if !bytes.Equal(traced[:baselineBytes], baseline) {
		t.Fatal("instrumented C output changed ordinary 60-frame GANO result")
	}
	cTrace, err := parseAnalysisMLPStageTrace(traced[baselineBytes:], uint32(frames*2), uint32(frames))
	if err != nil {
		t.Fatalf("parse GAML: %v", err)
	}
	cFrame := parseAnalysisMLPFirstGANOFrame(baseline)
	if cTrace.dense2Output != [2]uint32{cFrame.latest.musicProb, cFrame.latest.activityProb} {
		t.Fatalf("C dense2 output %08x/%08x is not linked to frame-0 raw analyzer-ring music/VAD %08x/%08x",
			cTrace.dense2Output[0], cTrace.dense2Output[1], cFrame.latest.musicProb, cFrame.latest.activityProb)
	}
	if cTrace.dense0Output != cTrace.gruInput {
		t.Fatal("C dense0 output does not match the actual GRU input")
	}
	if cTrace.gruStateAfter != cTrace.dense2Input {
		t.Fatal("C GRU output state does not match the actual dense2 input")
	}

	plain := NewTonalityAnalysisState(fs)
	plain.SetLSBDepth(lsbDepth)
	tracedState := NewTonalityAnalysisState(fs)
	tracedState.SetLSBDepth(lsbDepth)
	oldHook := analysisMLPTraceHook
	t.Cleanup(func() { analysisMLPTraceHook = oldHook })
	var goTrace analysisMLPTraceSnapshot
	var frame0LatestInfo AnalysisInfo
	goCalls := 0
	traceHook := func(snapshot analysisMLPTraceSnapshot) {
		goCalls++
		goTrace = snapshot
	}
	frameSamples := frameSize * channels
	var frame0TraceInfo AnalysisInfo
	for frameIndex := 0; frameIndex < frames; frameIndex++ {
		start := frameIndex * frameSamples
		frame := samples[start : start+frameSamples]
		analysisMLPTraceHook = nil
		plainInfo := plain.RunAnalysis(frame, frameSize, channels)
		analysisMLPTraceHook = traceHook
		tracedInfo := tracedState.RunAnalysis(frame, frameSize, channels)
		analysisMLPTraceHook = nil
		compareGoAnalysisFrame(t, frameIndex, tracedInfo, plainInfo, tracedState, plain)
		if frameIndex == 0 {
			frame0TraceInfo = tracedInfo
			frame0LatestInfo = analysisLatestRawInfo(tracedState)
		}
	}
	if goCalls != 1 || goTrace.Frame != 0 || goTrace.Dense0Calls != 1 || goTrace.GRUCalls != 1 || goTrace.Dense2Calls != 1 {
		t.Fatalf("Go MLP snapshot calls=%d metadata frame=%d dense0=%d GRU=%d dense2=%d",
			goCalls, goTrace.Frame, goTrace.Dense0Calls, goTrace.GRUCalls, goTrace.Dense2Calls)
	}
	if goTrace.Dense0Output != goTrace.GRUInput || goTrace.GRUStateAfter != goTrace.Dense2Input {
		t.Fatal("Go MLP snapshots do not follow the actual dense0→GRU→dense2 chain")
	}
	if got := [2]uint32{math.Float32bits(goTrace.Dense2Output[0]), math.Float32bits(goTrace.Dense2Output[1])}; got != [2]uint32{math.Float32bits(frame0LatestInfo.MusicProb), math.Float32bits(frame0LatestInfo.VADProb)} {
		t.Fatalf("Go dense2 output %08x/%08x is not linked to frame-0 raw analyzer-ring music/VAD %08x/%08x",
			got[0], got[1], math.Float32bits(frame0LatestInfo.MusicProb), math.Float32bits(frame0LatestInfo.VADProb))
	}
	infoDifference := diffAnalysisInfo(analysisInfoToOracle(frame0TraceInfo), cFrame.ret)
	if infoDifference != "" {
		t.Logf("frame-0 postprocessed GANI returned-info difference: %s", infoDifference)
	}

	var firstDifference string
	compareAnalysisMLPArray(t, &firstDifference, "corpus dense0 features", cTrace.dense0Input[:], goTrace.Dense0Input[:])
	compareAnalysisMLPArray(t, &firstDifference, "corpus dense0 output / GRU input", cTrace.dense0Output[:], goTrace.Dense0Output[:])
	compareAnalysisMLPArray(t, &firstDifference, "corpus GRU pre-state", cTrace.gruStateBefore[:], goTrace.GRUStateBefore[:])
	compareAnalysisMLPArray(t, &firstDifference, "corpus GRU post-state", cTrace.gruStateAfter[:], goTrace.GRUStateAfter[:])
	compareAnalysisMLPArray(t, &firstDifference, "corpus dense2 output", cTrace.dense2Output[:], goTrace.Dense2Output[:])
	if firstDifference == "" {
		t.Log("corpus_music_v1 frame-0 MLP stages match bitwise")
	} else {
		t.Logf("first corpus_music_v1 frame-0 MLP difference: %s", firstDifference)
	}
}
