package celt

import (
	"math"
	"testing"
)

func TestTransientInvIndexSanitizesNonFinite(t *testing.T) {
	if got := transientInvIndex(float32(math.NaN())); got != 0 {
		t.Fatalf("transientInvIndex(NaN) = %d, want 0", got)
	}
	if got := transientInvIndex(float32(math.Inf(-1))); got != 0 {
		t.Fatalf("transientInvIndex(-Inf) = %d, want 0", got)
	}
	if got := transientInvIndex(float32(math.Inf(1))); got != 127 {
		t.Fatalf("transientInvIndex(+Inf) = %d, want 127", got)
	}
	if got := transientInvIndex(-5); got != 0 {
		t.Fatalf("transientInvIndex(-5) = %d, want 0", got)
	}
	if got := transientInvIndex(42.9); got != 42 {
		t.Fatalf("transientInvIndex(42.9) = %d, want 42", got)
	}
}

func TestEncodeFrameNonFinitePCMNoPanic(t *testing.T) {
	enc := NewEncoder(1)
	pcm := make([]float32, 960)
	pcm[10] = float32(math.NaN())
	pcm[20] = float32(math.Inf(1))
	pcm[30] = float32(math.Inf(-1))
	if _, err := enc.EncodeFrame(pcm, 960); err != nil {
		t.Fatalf("EncodeFrame with non-finite PCM failed: %v", err)
	}
}

func TestValidFrameSizeRejectsUnsupportedSizes(t *testing.T) {
	dec48 := NewDecoder(1)
	if dec48.validFrameSize(1920) {
		t.Fatalf("48 kHz decoder unexpectedly accepted 1920-sample frame")
	}
	if !dec48.validFrameSize(960) {
		t.Fatalf("48 kHz decoder rejected valid 960-sample frame")
	}

	enc48 := NewEncoder(1)
	if enc48.validFrameSize(1920) {
		t.Fatalf("48 kHz encoder unexpectedly accepted 1920-sample frame")
	}

	enc96 := NewEncoder(1)
	enc96.sampleRate = 96000
	enc96.hd96kOverlap = 240
	if enc96.validFrameSize(120) {
		t.Fatalf("96 kHz HD encoder unexpectedly accepted 120-sample frame")
	}
	if !enc96.validFrameSize(1920) {
		t.Fatalf("96 kHz HD encoder rejected valid 1920-sample frame")
	}
}

func TestStereoAnalysisShortEdgesNoPanic(t *testing.T) {
	edges := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	norm := make([]celtNorm, 64)
	if stereoAnalysisDecision(norm, norm, 10, 1, edges) {
		t.Fatalf("expected false for <13 bands")
	}
}
