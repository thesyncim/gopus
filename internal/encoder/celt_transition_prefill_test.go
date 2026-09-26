package encoder

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
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
	enc.prepareCELTPCM(frame, frameSize)

	enc.maybePrefillCELTOnModeTransition(ModeCELT)

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
	enc.prepareCELTPCM(frame, frameSize)

	enc.maybePrefillCELTOnModeTransition(ModeCELT)

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
	enc.prepareCELTPCM(frame, frameSize)

	enc.maybePrefillCELTOnModeTransition(ModeCELT)

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
	enc.applyDelayCompensation(frame, frameSize)

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

	src := enc.celtTransitionPrefillSource(prefillFrameSize)
	if len(src) != prefillFrameSize {
		t.Fatalf("prefill source len=%d want=%d", len(src), prefillFrameSize)
	}
	for i := range prefillFrameSize {
		if want := origDelay[wantStart+i]; src[i] != want {
			t.Fatalf("prefill source[%d]=%.0f want delay history %.0f", i, src[i], want)
		}
	}

	enc.maybePrefillCELTOnModeTransition(ModeCELT)
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
	enc.prepareCELTPCM(frame, frameSize)

	enc.maybePrefillCELTOnModeTransition(ModeHybrid)

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
	enc.prepareCELTPCM(frame, frameSize)

	enc.maybePrefillCELTOnModeTransition(ModeCELT)

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

	enc.maybePrefillSILKOnModeTransition(ModeHybrid, true, true)

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

	enc.maybePrefillSILKOnModeTransition(ModeHybrid, false, false)

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
	newEnc := func() *Encoder {
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
		return enc
	}

	primed := newEnc()
	primed.maybePrefillSILKOnModeTransition(ModeHybrid, true, false)
	if primed.silk == nil || !primed.silkPrefillPending {
		t.Fatal("expected a staged SILK prefill after the CELT->Hybrid transition")
	}
	plain := newEnc()
	plain.ensureSILKEncoder()

	// Code the same stereo frame after the prefill and without it: the
	// prefill primes both SILK channels, so the packets differ.
	frame := make([]opusRes, 960*2)
	for i := range 960 {
		frame[2*i] = opusRes(0.3 * math.Sin(2*math.Pi*220*float64(i)/48000.0))
		frame[2*i+1] = opusRes(0.2 * math.Sin(2*math.Pi*330*float64(i)/48000.0))
	}
	var packets [2][]byte
	for i, enc := range []*Encoder{primed, plain} {
		enc.configureSILKMode(960, 32000, 1275*8, false)
		if err := enc.runPendingSILKPrefill(silk.VADNoDecision); err != nil {
			t.Fatalf("prefill: %v", err)
		}
		if enc.silkPrefillPending {
			t.Fatal("prefill still pending after runPendingSILKPrefill")
		}
		if got := enc.silkMode.NChannelsInternal; got != 2 {
			t.Fatalf("SILK codes %d channels, want 2", got)
		}
		buf := make([]byte, 1275)
		var re rangecoding.Encoder
		re.Init(buf)
		if _, err := enc.silk.Encode(&enc.silkMode, frame, 960, &re, 0, 1); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		n := (re.Tell() + 7) >> 3
		re.Done()
		packets[i] = buf[:n]
	}
	if bytes.Equal(packets[0], packets[1]) {
		t.Fatal("expected the stereo transition prefill to prime the SILK history")
	}
}
