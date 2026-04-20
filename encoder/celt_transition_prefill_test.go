package encoder

import (
	"math"
	"testing"
)

func makeTransitionPCM(frameSize, channels int) []float64 {
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		s := math.Sin(2 * math.Pi * 440 * float64(i) / 48000.0)
		for c := 0; c < channels; c++ {
			pcm[i*channels+c] = s
		}
	}
	return pcm
}

func TestCELTTransitionPrefillForcesOneIntraFrame(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.prevMode = ModeHybrid

	frameSize := 480
	frame := makeTransitionPCM(frameSize, 1)
	celtPCM := enc.prepareCELTPCM(frame, frameSize)

	enc.maybePrefillCELTOnModeTransition(ModeCELT, celtPCM, frameSize)

	if !enc.celtForceIntra {
		t.Fatal("expected celtForceIntra after mode-transition prefill")
	}
	if enc.celtEncoder == nil {
		t.Fatal("expected CELT encoder to be initialized for prefill")
	}

	if got := enc.celtPredictionModeForFrame(); got != 0 {
		t.Fatalf("celtPredictionModeForFrame() first call = %d, want 0", got)
	}
	if enc.celtForceIntra {
		t.Fatal("expected celtForceIntra to be consumed after first frame mode query")
	}
	if got := enc.celtPredictionModeForFrame(); got != enc.celtPredictionMode() {
		t.Fatalf("celtPredictionModeForFrame() second call = %d, want default prediction mode", got)
	}
}

func TestCELTTransitionPrefillSkippedInLowDelay(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.lowDelay = true
	enc.prevMode = ModeHybrid

	frameSize := 480
	frame := makeTransitionPCM(frameSize, 1)
	celtPCM := enc.prepareCELTPCM(frame, frameSize)

	enc.maybePrefillCELTOnModeTransition(ModeCELT, celtPCM, frameSize)

	if enc.celtForceIntra {
		t.Fatal("did not expect celtForceIntra in low-delay mode")
	}
	if enc.celtEncoder != nil && enc.celtEncoder.FrameCount() != 0 {
		t.Fatal("did not expect CELT prefill frame in low-delay mode")
	}
}

func TestCELTTransitionPrefillSkippedWithoutModeChange(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.prevMode = ModeCELT

	frameSize := 480
	frame := makeTransitionPCM(frameSize, 1)
	celtPCM := enc.prepareCELTPCM(frame, frameSize)

	enc.maybePrefillCELTOnModeTransition(ModeCELT, celtPCM, frameSize)

	if enc.celtForceIntra {
		t.Fatal("did not expect celtForceIntra when mode is unchanged")
	}
	if enc.celtEncoder != nil && enc.celtEncoder.FrameCount() != 0 {
		t.Fatal("did not expect CELT prefill frame when mode is unchanged")
	}
}

func TestCELTTransitionPrefillSnapshotsLibopusDelayHistoryWindow(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.prevMode = ModeHybrid

	frameSize := 480
	encoderBuffer := enc.sampleRate / 100
	delayComp := enc.sampleRate / 250
	prefillFrameSize := enc.sampleRate / 400
	if encoderBuffer <= 0 || delayComp <= 0 || prefillFrameSize <= 0 {
		t.Fatal("invalid test setup")
	}

	enc.delayBuffer = make([]float64, encoderBuffer)
	for i := range enc.delayBuffer {
		enc.delayBuffer[i] = float64(i + 1)
	}
	origDelay := append([]float64(nil), enc.delayBuffer...)

	frame := make([]float64, frameSize)
	for i := range frame {
		frame[i] = 10000 + float64(i)
	}
	celtPCM := enc.applyDelayCompensation(frame, frameSize)

	wantStart := encoderBuffer - delayComp - prefillFrameSize
	if wantStart < 0 {
		t.Fatalf("invalid prefill window: start=%d", wantStart)
	}
	if len(enc.scratchTransitionPrefill) != prefillFrameSize {
		t.Fatalf("prefill snapshot len=%d want=%d", len(enc.scratchTransitionPrefill), prefillFrameSize)
	}
	for i := 0; i < prefillFrameSize; i++ {
		got := enc.scratchTransitionPrefill[i]
		want := origDelay[wantStart+i]
		if got != want {
			t.Fatalf("prefill[%d]=%.0f want %.0f", i, got, want)
		}
	}

	enc.maybePrefillCELTOnModeTransition(ModeCELT, celtPCM, frameSize)
	if !enc.celtForceIntra {
		t.Fatal("expected celtForceIntra after transition prefill")
	}
	if enc.celtEncoder == nil || enc.celtEncoder.FrameCount() != 1 {
		t.Fatal("expected one CELT prefill frame")
	}
}

func TestCELTTransitionPrefillResyncsAnalysisAfterReset(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.prevMode = ModeCELT
	enc.lastAnalysisValid = true
	enc.lastAnalysisInfo = AnalysisInfo{
		BandwidthIndex: 13,
		Activity:       0.75,
		TonalitySlope:  0.2,
		MaxPitchRatio:  1.0,
	}

	frameSize := 480
	frame := makeTransitionPCM(frameSize, 1)
	celtPCM := enc.prepareCELTPCM(frame, frameSize)

	enc.maybePrefillCELTOnModeTransition(ModeHybrid, celtPCM, frameSize)

	if enc.celtEncoder == nil {
		t.Fatal("expected CELT encoder to be initialized for prefill")
	}
	if got := enc.celtEncoder.AnalysisBandwidth(); got != 13 {
		t.Fatalf("AnalysisBandwidth() after prefill = %d, want 13", got)
	}
}

func TestCELTTransitionPrefillSkipsWhenDelayedTransitionAlreadyAdvancedPrevMode(t *testing.T) {
	enc := NewEncoder(48000, 1)
	// After a long hybrid->CELT transition packet, libopus advances prev_mode to
	// CELT even though the previous packet TOC still says hybrid.
	enc.prevMode = ModeCELT
	enc.prevPacketMode = ModeHybrid

	frameSize := 960
	frame := makeTransitionPCM(frameSize, 1)
	celtPCM := enc.prepareCELTPCM(frame, frameSize)

	enc.maybePrefillCELTOnModeTransition(ModeCELT, celtPCM, frameSize)

	if enc.celtForceIntra {
		t.Fatal("did not expect celtForceIntra after delayed transition already completed")
	}
	if enc.celtEncoder != nil && enc.celtEncoder.FrameCount() != 0 {
		t.Fatal("did not expect CELT prefill when prevMode is already CELT")
	}
}

func TestSilkTransitionPrefillLongPacketKeepsFirstCELTSnapshot(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.prevMode = ModeCELT
	enc.prevPacketMode = ModeCELT

	prefillSamples := enc.sampleRate / 100
	enc.delayBuffer = make([]float64, prefillSamples)
	for i := range enc.delayBuffer {
		enc.delayBuffer[i] = float64(i + 1)
	}

	enc.maybePrefillSILKOnModeTransitionWithOptions(ModeHybrid, false, true)

	if !enc.hasCELTPrefill {
		t.Fatal("expected first long-packet prefill to capture CELT transition history")
	}
	if len(enc.scratchCELTPrefill) == 0 {
		t.Fatal("expected CELT prefill snapshot")
	}
	want := append([]float64(nil), enc.scratchCELTPrefill...)

	for i := range enc.delayBuffer {
		enc.delayBuffer[i] = float64(1000 + i)
	}

	enc.maybePrefillSILKOnModeTransitionWithOptions(ModeHybrid, true, false)

	if !enc.hasCELTPrefill {
		t.Fatal("expected later long-packet prefill to keep prior CELT snapshot")
	}
	if len(enc.scratchCELTPrefill) != len(want) {
		t.Fatalf("scratchCELTPrefill len=%d want=%d", len(enc.scratchCELTPrefill), len(want))
	}
	for i := range want {
		if got := enc.scratchCELTPrefill[i]; got != want[i] {
			t.Fatalf("scratchCELTPrefill[%d]=%f want %f", i, got, want[i])
		}
	}
}
