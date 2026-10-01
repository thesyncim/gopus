//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

func TestAnalysisNonzeroHistoryAvgModTrace(t *testing.T) {
	const (
		fs        = 48000
		channels  = 2
		frameSize = 960
		frames    = 50
		lsbDepth  = 24
		frame1    = uint32(1)
		frame2    = uint32(2)
	)
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "analysis nonzero-history avg_mod trace")

	samples, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1,
		fs,
		frameSize*channels*frames,
		channels,
	)
	if err != nil {
		t.Fatalf("generate AM multisine analysis input: %v", err)
	}
	samples = quantizeCELTTracePCM(samples)
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		fs, channels, frameSize, frames, lsbDepth,
		0, ^uint32(1), 0, uint32(len(samples)),
	)
	payload.Float32s(samples...)
	input := payload.Bytes()
	assertAnalysisAvgModInputSHA256(t, "AM multisine with quantizeCELTTracePCM", input,
		"58fad87922a34dede4b4e576be6b0bdcb414f4db8007b409e302982e4f0c396c")

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	if err := validateAnalysisGANO(baseline, frames); err != nil {
		t.Fatalf("baseline GANO structure: %v", err)
	}

	traces := make(map[uint32]libopusAnalysisStageTrace, 2)
	phaseTraces := make(map[uint32]libopusAnalysisPhaseTrace, 2)
	for _, targetFrame := range []uint32{frame1, frame2} {
		helperPath, sourceHash := buildLibopusAnalysisStageTraceHelper(t, targetFrame)
		traced, err := libopustest.RunHelper(helperPath, input)
		if err != nil {
			libopustest.HelperUnavailable(t, fmt.Sprintf("analysis frame-%d stage trace", targetFrame), err)
		}
		if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
			t.Fatalf("frame-%d source-instrumented helper changed baseline GANO output", targetFrame)
		}
		trace, phase, err := parseLibopusAnalysisStageTrace(traced[len(baseline):], targetFrame)
		if err != nil {
			t.Fatalf("parse frame-%d GAST/GAPH: %v", targetFrame, err)
		}
		validateAnalysisNonzeroHistoryCapture(t, trace, phase, targetFrame, frames, sourceHash)
		traces[targetFrame] = trace
		phaseTraces[targetFrame] = phase
	}

	plainState := NewTonalityAnalysisState(fs)
	plainState.SetLSBDepth(lsbDepth)
	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(lsbDepth)
	var goFrame1 analysisNonzeroHistoryGoFrame
	var goFrame2 analysisNonzeroHistoryGoFrame
	var goMetrics [239]analysisPerBinTraceSnapshot
	var goMetricCalls int
	var goMetricOverflow bool
	var frame2CountBefore, frame2CountAfter int32
	var plainFrame2CountBefore, plainFrame2CountAfter int32
	oldPerBinHook := analysisPerBinTraceHook
	t.Cleanup(func() { analysisPerBinTraceHook = oldPerBinHook })

	chunkSize := fs / 50
	chunkCount := (frameSize + chunkSize - 1) / chunkSize
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSize * channels
		framePCM := samples[start : start+frameSize*channels]
		if frame == int(frame2) && (state.Count != int32(frame2) || plainState.Count != int32(frame2)) {
			t.Fatalf("frame-2 pre-run Count traced=%d plain=%d want %d", state.Count, plainState.Count, frame2)
		}
		if frame == int(frame2) {
			frame2CountBefore, plainFrame2CountBefore = state.Count, plainState.Count
		}

		// Keep the observer off for the plain state on every call. It is active
		// only for the selected frame-2 run on the traced state.
		analysisPerBinTraceHook = nil
		plainInfo := plainState.RunAnalysis(framePCM, frameSize, channels)
		if frame == int(frame2) {
			analysisPerBinTraceHook = func(snapshot analysisPerBinTraceSnapshot) {
				if goMetricCalls >= len(goMetrics) || snapshot.Bin != int32(goMetricCalls+1) {
					goMetricOverflow = true
					return
				}
				goMetrics[goMetricCalls] = snapshot
				goMetricCalls++
			}
		}
		tracedInfo := state.RunAnalysis(framePCM, frameSize, channels)
		analysisPerBinTraceHook = nil
		if frame == int(frame2) {
			frame2CountAfter, plainFrame2CountAfter = state.Count, plainState.Count
		}
		compareAnalysisPerBinGoTransparency(t, frame, tracedInfo, plainInfo, state, plainState)

		switch frame {
		case int(frame1):
			goFrame1 = snapshotAnalysisNonzeroHistoryGoFrame(state)
		case int(frame2):
			goFrame2 = snapshotAnalysisNonzeroHistoryGoFrame(state)
		}
	}
	if goMetricOverflow || goMetricCalls != len(goMetrics) {
		t.Fatalf("frame-2 Go per-bin metric calls=%d overflow=%t want 239/false", goMetricCalls, goMetricOverflow)
	}
	if frame2CountBefore != 2 || plainFrame2CountBefore != 2 || frame2CountAfter != 3 || plainFrame2CountAfter != 3 {
		t.Fatalf("frame-2 Count traced=%d->%d plain=%d->%d want 2->3", frame2CountBefore, frame2CountAfter,
			plainFrame2CountBefore, plainFrame2CountAfter)
	}
	if chunkCount != 1 {
		t.Fatalf("selected public frame has %d tonality chunks want 1", chunkCount)
	}

	frame1Trace, frame1Phase := traces[frame1], phaseTraces[frame1]
	compareAnalysisNonzeroHistoryFrame(t, frame1Trace, frame1Phase, goFrame1, frame1)
	frame2Trace, frame2Phase := traces[frame2], phaseTraces[frame2]
	compareAnalysisNonzeroHistoryFrame(t, frame2Trace, frame2Phase, goFrame2, frame2)

	priorNonzero := countNonzeroAnalysisPhaseField(frame1Phase, func(record libopusAnalysisPhaseRecord) uint32 {
		return record.d2AngleState
	})
	if priorNonzero == 0 {
		t.Fatal("frame-1 C phase state has no nonzero d2 history for frame 2")
	}
	cNonzeroAvgMod := countNonzeroAnalysisPhaseField(frame2Phase, func(record libopusAnalysisPhaseRecord) uint32 {
		return record.avgMod
	})
	goNonzeroAvgMod := 0
	for _, metric := range goMetrics {
		if metric.AvgMod != 0 {
			goNonzeroAvgMod++
		}
	}
	if cNonzeroAvgMod == 0 || goNonzeroAvgMod == 0 {
		t.Fatalf("frame-2 avg_mod does not exercise nonzero history: C=%d Go=%d bins", cNonzeroAvgMod, goNonzeroAvgMod)
	}
	t.Logf("frame-1 d2 history is nonzero in %d/239 bins; frame-2 avg_mod is nonzero in C=%d/239 Go=%d/239 bins",
		priorNonzero, cNonzeroAvgMod, goNonzeroAvgMod)
	logAnalysisNonzeroHistoryMetricDifferences(t, frame2Phase, goMetrics[:], frame2)
	logAnalysisNonzeroHistoryAvgModBin40(t, frame1Phase.records[39], frame2Phase.records[39], goMetrics[39])
}

func TestAnalysisNonzeroHistoryAvgModMixed5msTrace(t *testing.T) {
	runAnalysisNonzeroHistoryAvgModTrace(t, testsignal.CorpusMixedV1,
		48000, 2, 240, 240, 11, 24, false,
		"c9a3382a2990a523ec7570337e5165b0704b39a9f3ea2b51feb8f20e2a715603")
}

func TestAnalysisNonzeroHistoryAvgModCastanet16kTrace(t *testing.T) {
	runAnalysisNonzeroHistoryAvgModTrace(t, testsignal.CorpusCastanetTransientV1,
		16000, 1, 320, 60, 2, 24, false,
		"c7006d837316da4decefd30b00c7bb5e2c64d5ad70cff724662f79b1caf6054b")
}

func runAnalysisNonzeroHistoryAvgModTrace(t *testing.T, class string, fs, channels, frameSize, frames, targetFrame, lsbDepth int, clamp bool, wantInputSHA256 string) {
	t.Helper()
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "nonzero-history avg_mod trace")

	samples, err := testsignal.GenerateCorpusSignal(class,
		fs, frames*frameSize*channels, channels)
	if err != nil {
		t.Fatalf("generate %s analysis input: %v", class, err)
	}
	if clamp {
		clampToOpusDemoF32InPlace(samples)
	}
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		uint32(fs), uint32(channels), uint32(frameSize), uint32(frames), uint32(lsbDepth),
		0, ^uint32(1), 0, uint32(len(samples)))
	payload.Float32s(samples...)
	input := payload.Bytes()
	assertAnalysisAvgModInputSHA256(t, fmt.Sprintf("%s clamp=%t", class, clamp), input, wantInputSHA256)

	baselinePath, err := libopusAnalysisHelper.Path(buildLibopusAnalysisHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	baseline, err := libopustest.RunHelper(baselinePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "baseline tonality analysis", err)
	}
	if err := validateAnalysisGANO(baseline, frames); err != nil {
		t.Fatalf("baseline GANO structure: %v", err)
	}
	tracePath, sourceHash := buildLibopusAnalysisStageTraceHelper(t, uint32(targetFrame))
	traced, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis phase trace", err)
	}
	if len(traced) < len(baseline) || !bytes.Equal(traced[:len(baseline)], baseline) {
		t.Fatal("source-instrumented analysis helper changed baseline GANO output")
	}
	trace, phaseTrace, err := parseLibopusAnalysisStageTrace(traced[len(baseline):], uint32(targetFrame))
	if err != nil {
		t.Fatalf("parse GAST/GAPH: %v", err)
	}
	if trace.frame != uint32(targetFrame) || trace.runCalls != uint32(frames) || trace.tonalityCalls != 1 ||
		trace.overflow != 0 || trace.stageMask != 15 || trace.sourceHash != sourceHash {
		t.Fatalf("GAST frame=%d run=%d tonality=%d overflow=%d stages=%04b source=%s want=%s",
			trace.frame, trace.runCalls, trace.tonalityCalls, trace.overflow, trace.stageMask,
			trace.sourceHash, sourceHash)
	}
	if phaseTrace.frame != uint32(targetFrame) || phaseTrace.totalCalls != 239 || phaseTrace.storedCalls != 239 ||
		phaseTrace.overflow != 0 || len(phaseTrace.records) != 239 {
		t.Fatalf("GAPH frame=%d calls=%d stored=%d overflow=%d records=%d",
			phaseTrace.frame, phaseTrace.totalCalls, phaseTrace.storedCalls,
			phaseTrace.overflow, len(phaseTrace.records))
	}

	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(lsbDepth)
	var metrics [239]analysisPerBinTraceSnapshot
	metricCalls := 0
	oldHook := analysisPerBinTraceHook
	t.Cleanup(func() { analysisPerBinTraceHook = oldHook })
	for frame := 0; frame <= targetFrame; frame++ {
		start := frame * frameSize * channels
		framePCM := samples[start : start+frameSize*channels]
		if frame == targetFrame {
			if state.Count != 2 {
				t.Fatalf("%s frame-%d pre-analysis Count=%d want 2", class, frame, state.Count)
			}
			nonzeroPriorHistory := 0
			for _, value := range state.D2Angle {
				if value != 0 {
					nonzeroPriorHistory++
				}
			}
			if nonzeroPriorHistory == 0 {
				t.Fatalf("%s frame-%d has no nonzero prior d2 history", class, frame)
			}
			analysisPerBinTraceHook = func(snapshot analysisPerBinTraceSnapshot) {
				if metricCalls >= len(metrics) || snapshot.Bin != int32(metricCalls+1) {
					metricCalls = len(metrics) + 1
					return
				}
				metrics[metricCalls] = snapshot
				metricCalls++
			}
		} else {
			analysisPerBinTraceHook = nil
		}
		state.RunAnalysis(framePCM, frameSize, channels)
	}
	analysisPerBinTraceHook = nil
	if metricCalls != len(metrics) {
		t.Fatalf("%s frame-%d Go per-bin trace calls=%d want %d", class, targetFrame, metricCalls, len(metrics))
	}
	if state.Count != 3 {
		t.Fatalf("%s frame-%d post-analysis Count=%d want 3", class, targetFrame, state.Count)
	}
	compareAnalysisComplexBits(t, "target-frame FFT output", trace.fftOutput, state.scratchFFTOut[:])
	compareAnalysisPhaseInputs(t, phaseTrace, state.scratchFFTOut[:])
	compareAnalysisPhaseState(t, phaseTrace, state.Angle, state.DAngle, state.D2Angle)
	compareAnalysisPerBinMetrics(t, phaseTrace, metrics[:])
}

func assertAnalysisAvgModInputSHA256(t *testing.T, label string, input []byte, want string) {
	t.Helper()
	got := fmt.Sprintf("%x", sha256.Sum256(input))
	if got != want {
		t.Fatalf("%s GANI input SHA256=%s want %s", label, got, want)
	}
	t.Logf("%s GANI input SHA256=%s", label, got)
}

type analysisNonzeroHistoryGoFrame struct {
	inmem         [480]float32
	fftInput      []complex64
	fftOutput     []complex64
	angle         [240]float32
	dAngle        [240]float32
	d2Angle       [240]float32
	downmixState  [3]float32
	hpEnergyAccum float32
	memFill       int32
}

func snapshotAnalysisNonzeroHistoryGoFrame(state *TonalityAnalysisState) analysisNonzeroHistoryGoFrame {
	var frame analysisNonzeroHistoryGoFrame
	copy(frame.inmem[:], state.InMem[240:720])
	frame.fftInput = append([]complex64(nil), state.scratchFFTIn[:]...)
	frame.fftOutput = append([]complex64(nil), state.scratchFFTOut[:]...)
	frame.angle = state.Angle
	frame.dAngle = state.DAngle
	frame.d2Angle = state.D2Angle
	frame.downmixState = state.DownmixState
	frame.hpEnergyAccum = state.HPEnerAccum
	frame.memFill = state.MemFill
	return frame
}

func validateAnalysisNonzeroHistoryCapture(t *testing.T, trace libopusAnalysisStageTrace, phase libopusAnalysisPhaseTrace, targetFrame uint32, frames int, sourceHash string) {
	t.Helper()
	if trace.frame != targetFrame || trace.runCalls != uint32(frames) || trace.tonalityCalls != 1 ||
		trace.overflow != 0 || trace.stageMask != 15 || trace.sourceHash != sourceHash {
		t.Fatalf("GAST target=%d frame=%d run=%d tonality=%d overflow=%d stages=%04b source=%s want=%s",
			targetFrame, trace.frame, trace.runCalls, trace.tonalityCalls, trace.overflow, trace.stageMask,
			trace.sourceHash, sourceHash)
	}
	wantMetadata := [21]uint32{
		960, 960, 48000, 0, ^uint32(1), 2, 24,
		48000, 480, 0, 0, ^uint32(1), 2, 24,
		240, 240, 480, 480, 480, 3, 1,
	}
	if trace.metadata != wantMetadata {
		t.Fatalf("frame-%d GAST source geometry=%v want %v", targetFrame, trace.metadata, wantMetadata)
	}
	if phase.frame != targetFrame || phase.totalCalls != 239 || phase.storedCalls != 239 ||
		phase.overflow != 0 || len(phase.records) != 239 {
		t.Fatalf("frame-%d GAPH frame=%d calls=%d stored=%d overflow=%d records=%d",
			targetFrame, phase.frame, phase.totalCalls, phase.storedCalls, phase.overflow, len(phase.records))
	}
}

func compareAnalysisNonzeroHistoryFrame(t *testing.T, trace libopusAnalysisStageTrace, phase libopusAnalysisPhaseTrace, goFrame analysisNonzeroHistoryGoFrame, targetFrame uint32) {
	t.Helper()
	if goFrame.memFill != 240 {
		t.Fatalf("frame-%d Go post-run mem_fill=%d want 240", targetFrame, goFrame.memFill)
	}
	compareAnalysisF32Bits(t, fmt.Sprintf("frame-%d downmix/resampler output ring", targetFrame), trace.inmem, goFrame.inmem[:])
	compareAnalysisComplexBits(t, fmt.Sprintf("frame-%d windowed FFT input", targetFrame), trace.fftInput, goFrame.fftInput)
	compareAnalysisComplexBits(t, fmt.Sprintf("frame-%d FFT output", targetFrame), trace.fftOutput, goFrame.fftOutput)
	compareAnalysisPhaseInputs(t, phase, goFrame.fftOutput)
	compareAnalysisPhaseState(t, phase, goFrame.angle, goFrame.dAngle, goFrame.d2Angle)
	compareAnalysisF32Bits(t, fmt.Sprintf("frame-%d downmix state", targetFrame), trace.downmixState[:], goFrame.downmixState[:])
	if got := math.Float32bits(goFrame.hpEnergyAccum); got != trace.hpEnergyAccum {
		t.Fatalf("frame-%d post-run high-pass energy Go=%08x C=%08x", targetFrame, got, trace.hpEnergyAccum)
	}
	t.Logf("frame-%d GAST input/window/FFT and GAPH phase inputs/state match", targetFrame)
}

func countNonzeroAnalysisPhaseField(trace libopusAnalysisPhaseTrace, field func(libopusAnalysisPhaseRecord) uint32) int {
	count := 0
	for _, record := range trace.records {
		if math.Float32frombits(field(record)) != 0 {
			count++
		}
	}
	return count
}

func logAnalysisNonzeroHistoryMetricDifferences(t *testing.T, trace libopusAnalysisPhaseTrace, goMetrics []analysisPerBinTraceSnapshot, frame uint32) {
	t.Helper()
	if len(trace.records) != len(goMetrics) {
		t.Fatalf("frame-%d per-bin metric dimensions C=%d Go=%d", frame, len(trace.records), len(goMetrics))
	}
	fields := []struct {
		name string
		c    func(libopusAnalysisPhaseRecord) uint32
		goAt func(analysisPerBinTraceSnapshot) float32
	}{
		{"avg_mod", func(r libopusAnalysisPhaseRecord) uint32 { return r.avgMod }, func(s analysisPerBinTraceSnapshot) float32 { return s.AvgMod }},
		{"tonality2", func(r libopusAnalysisPhaseRecord) uint32 { return r.tonality2 }, func(s analysisPerBinTraceSnapshot) float32 { return s.Tonality2 }},
		{"raw_tonality", func(r libopusAnalysisPhaseRecord) uint32 { return r.rawTonality }, func(s analysisPerBinTraceSnapshot) float32 { return s.Tonality }},
		{"noisiness", func(r libopusAnalysisPhaseRecord) uint32 { return r.noisiness }, func(s analysisPerBinTraceSnapshot) float32 { return s.Noisiness }},
	}
	for _, field := range fields {
		firstBin := -1
		var firstGo, firstC uint32
		differences := 0
		for i, record := range trace.records {
			if record.bin != uint32(i+1) || goMetrics[i].Bin != int32(i+1) {
				t.Fatalf("frame-%d %s bin order C=%d Go=%d expected=%d", frame, field.name, record.bin, goMetrics[i].Bin, i+1)
			}
			goBits := math.Float32bits(field.goAt(goMetrics[i]))
			cBits := field.c(record)
			if goBits != cBits {
				if firstBin < 0 {
					firstBin, firstGo, firstC = int(record.bin), goBits, cBits
				}
				differences++
			}
		}
		if differences != 0 {
			t.Fatalf("frame-%d per-bin %s first difference bin=%d Go=%08x C=%08x differing bins=%d/239",
				frame, field.name, firstBin, firstGo, firstC, differences)
		}
		t.Logf("frame-%d per-bin %s exact for bins 1..239", frame, field.name)
	}
}

func logAnalysisNonzeroHistoryAvgModBin40(t *testing.T, prior, current libopusAnalysisPhaseRecord, goMetric analysisPerBinTraceSnapshot) {
	t.Helper()
	if prior.bin != 40 || current.bin != 40 || goMetric.Bin != 40 {
		t.Fatalf("avg_mod diagnostic bin selection C-prior=%d C-current=%d Go=%d want 40", prior.bin, current.bin, goMetric.Bin)
	}
	oldAngle := math.Float32frombits(prior.angleState)
	oldDAngle := math.Float32frombits(prior.dAngleState)
	oldD2Angle := math.Float32frombits(prior.d2AngleState)
	angle := math.Float32frombits(current.angle)
	angle2 := math.Float32frombits(current.angle2)

	// These noinline float32 operations model the source's individual C-float
	// rounding points. They are diagnostic models, not captured intermediates
	// and not replacements for either implementation's actual avg_mod.
	dAngleModel := analysisAvgModModelSub32(angle, oldAngle)
	d2AngleModel := analysisAvgModModelSub32(dAngleModel, oldDAngle)
	dAngle2Model := analysisAvgModModelSub32(angle2, angle)
	d2Angle2Model := analysisAvgModModelSub32(dAngle2Model, dAngleModel)
	mod1RootModel := analysisAvgModModelSub32(d2AngleModel, float32(analysisFloat2Int(d2AngleModel)))
	mod2RootModel := analysisAvgModModelSub32(d2Angle2Model, float32(analysisFloat2Int(d2Angle2Model)))
	mod1SquareModel := analysisAvgModModelMul32(mod1RootModel, mod1RootModel)
	mod1FourthModel := analysisAvgModModelMul32(mod1SquareModel, mod1SquareModel)
	mod2SquareModel := analysisAvgModModelMul32(mod2RootModel, mod2RootModel)
	mod2FourthModel := analysisAvgModModelMul32(mod2SquareModel, mod2SquareModel)
	twoMod2Model := analysisAvgModModelMul32(2, mod2FourthModel)
	sumWithOldHistoryModel := analysisAvgModModelAdd32(oldD2Angle, mod1FourthModel)
	sumAllTermsModel := analysisAvgModModelAdd32(sumWithOldHistoryModel, twoMod2Model)
	avgModSourceOrderModel := analysisAvgModModelMul32(0.25, sumAllTermsModel)
	avgModGoFMA32Model := analysisAvgMod32(mod1SquareModel, oldD2Angle, mod2FourthModel)

	t.Logf("frame-2 avg_mod source model bin=40 C-input X1=(%08x,%08x) X2=(%08x,%08x) angle=%08x angle2=%08x",
		current.x1r, current.x1i, current.x2r, current.x2i, current.angle, current.angle2)
	t.Logf("frame-2 avg_mod source model bin=40 prior A=%08x dA=%08x oldD2=%08x; modeled d_angle=%08x d2_angle=%08x d_angle2=%08x d2_angle2=%08x",
		prior.angleState, prior.dAngleState, prior.d2AngleState, math.Float32bits(dAngleModel),
		math.Float32bits(d2AngleModel), math.Float32bits(dAngle2Model), math.Float32bits(d2Angle2Model))
	t.Logf("frame-2 avg_mod source model bin=40 mod1_root=%08x mod1_square=%08x mod1_fourth=%08x mod2_root=%08x mod2_square=%08x mod2_fourth=%08x",
		math.Float32bits(mod1RootModel), math.Float32bits(mod1SquareModel), math.Float32bits(mod1FourthModel),
		math.Float32bits(mod2RootModel), math.Float32bits(mod2SquareModel), math.Float32bits(mod2FourthModel))
	t.Logf("frame-2 avg_mod source-order model bin=40 oldD2+mod1^4=%08x then +2*mod2^4=%08x then *0.25=%08x; actual avg_mod C=%08x Go=%08x",
		math.Float32bits(sumWithOldHistoryModel), math.Float32bits(sumAllTermsModel), math.Float32bits(avgModSourceOrderModel),
		current.avgMod, math.Float32bits(goMetric.AvgMod))
	t.Logf("frame-2 float32 whole-bin FMA model bin=40=%08x; original C avg_mod=%08x",
		math.Float32bits(avgModGoFMA32Model), current.avgMod)
}

//go:noinline
func analysisAvgModModelSub32(a, b float32) float32 {
	return a - b
}

//go:noinline
func analysisAvgModModelAdd32(a, b float32) float32 {
	return a + b
}

//go:noinline
func analysisAvgModModelMul32(a, b float32) float32 {
	return a * b
}
