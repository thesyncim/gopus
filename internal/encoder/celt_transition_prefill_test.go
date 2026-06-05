package encoder

import (
	"math"
	"testing"
)

func makeTransitionPCM(frameSize, channels int) []opusRes {
	pcm := make([]opusRes, frameSize*channels)
	for i := range frameSize {
		s := math.Sin(2 * math.Pi * 440 * float64(i) / 48000.0)
		for c := range channels {
			pcm[i*channels+c] = opusRes(s)
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
	encoderBuffer := int(enc.sampleRate) / 100
	delayComp := int(enc.sampleRate) / 250
	prefillFrameSize := int(enc.sampleRate) / 400
	if encoderBuffer <= 0 || delayComp <= 0 || prefillFrameSize <= 0 {
		t.Fatal("invalid test setup")
	}

	enc.delayBuffer = make([]opusRes, encoderBuffer)
	for i := range enc.delayBuffer {
		enc.delayBuffer[i] = opusRes(i + 1)
	}
	origDelay := append([]opusRes(nil), enc.delayBuffer...)

	frame := make([]opusRes, frameSize)
	for i := range frame {
		frame[i] = opusRes(10000 + i)
	}
	celtPCM := enc.applyDelayCompensation(frame, frameSize)

	wantStart := encoderBuffer - delayComp - prefillFrameSize
	if wantStart < 0 {
		t.Fatalf("invalid prefill window: start=%d", wantStart)
	}
	if len(enc.scratchTransitionPrefill) != prefillFrameSize {
		t.Fatalf("prefill snapshot len=%d want=%d", len(enc.scratchTransitionPrefill), prefillFrameSize)
	}
	for i := range prefillFrameSize {
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

	prefillSamples := int(enc.sampleRate) / 100
	enc.delayBuffer = make([]opusRes, prefillSamples)
	for i := range enc.delayBuffer {
		enc.delayBuffer[i] = opusRes(i + 1)
	}

	enc.maybePrefillSILKOnModeTransitionWithOptions(ModeHybrid, false, true)

	if !enc.hasCELTPrefill {
		t.Fatal("expected first long-packet prefill to capture CELT transition history")
	}
	if len(enc.scratchCELTPrefill) == 0 {
		t.Fatal("expected CELT prefill snapshot")
	}
	want := append([]opusRes(nil), enc.scratchCELTPrefill...)

	for i := range enc.delayBuffer {
		enc.delayBuffer[i] = opusRes(1000 + i)
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

func TestSilkTransitionPrefillStereoPrimesMidAndSide(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.prevMode = ModeCELT
	enc.prevPacketMode = ModeCELT
	enc.SetBitrate(64000)

	prefillSamples := int(enc.sampleRate) / 100
	enc.delayBuffer = make([]opusRes, prefillSamples*2)
	for i := range prefillSamples {
		left := 0.45 * math.Sin(2*math.Pi*440*float64(i)/48000.0)
		right := 0.20 * math.Sin(2*math.Pi*660*float64(i)/48000.0)
		enc.delayBuffer[2*i] = opusRes(left)
		enc.delayBuffer[2*i+1] = opusRes(right)
	}

	enc.maybePrefillSILKOnModeTransitionWithOptions(ModeHybrid, false, false)

	if enc.silkEncoder == nil {
		t.Fatal("expected mid SILK encoder after stereo transition prefill")
	}
	if enc.silkSideEncoder == nil {
		t.Fatal("expected side SILK encoder after stereo transition prefill")
	}
	if !hasNonZeroFloat32(enc.silkEncoder.InputBuffer()) {
		t.Fatal("expected stereo transition prefill to prime mid SILK history")
	}
	if !hasNonZeroFloat32(enc.silkSideEncoder.InputBuffer()) {
		t.Fatal("expected stereo transition prefill to prime side SILK history")
	}
	if enc.silkVADMidFeedback == nil || enc.silkVADSide == nil {
		t.Fatal("expected stereo transition prefill to run mid and side VAD")
	}
}

func hasNonZeroFloat32(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return true
		}
	}
	return false
}
