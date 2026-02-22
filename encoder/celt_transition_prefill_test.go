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
