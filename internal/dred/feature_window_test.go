package dred

import (
	"reflect"
	"testing"
)

func TestResultFeatureWindowRecoverable(t *testing.T) {
	payload := makeHeaderPayloadForTest(t, 6, 3, 9, 0, 0, 4)
	parsed, err := ParsePayload(payload, 0)
	if err != nil {
		t.Fatalf("ParsePayload error: %v", err)
	}
	result := parsed.ForRequest(Request{MaxDREDSamples: 960, SampleRate: 48000})
	window := result.FeatureWindow(960, 960, 0)

	if window.FeaturesPerFrame != 2 || window.NeededFeatureFrames != 2 {
		t.Fatalf("FeatureWindow frame counts=(%d,%d) want (2,2)", window.FeaturesPerFrame, window.NeededFeatureFrames)
	}
	if window.FeatureOffsetBase != 1 || window.MaxFeatureIndex != 3 {
		t.Fatalf("FeatureWindow offsets=(base=%d max=%d) want (1,3)", window.FeatureOffsetBase, window.MaxFeatureIndex)
	}
	if window.RecoverableFeatureFrames != 2 || window.MissingPositiveFrames != 0 {
		t.Fatalf("FeatureWindow recoverable/missing=(%d,%d) want (2,0)", window.RecoverableFeatureFrames, window.MissingPositiveFrames)
	}
	got := make([]int, 2)
	if n := window.FillFeatureOffsets(got); n != len(got) {
		t.Fatalf("FillFeatureOffsets count=%d want %d", n, len(got))
	}
	want := []int{1, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FillFeatureOffsets=%v want %v", got, want)
	}
}

func TestResultFeatureWindowMissingPositiveAndNegative(t *testing.T) {
	payload := makeHeaderPayloadForTest(t, 6, 3, 9, 0, 8, -4)
	parsed, err := ParsePayload(payload, 8)
	if err != nil {
		t.Fatalf("ParsePayload error: %v", err)
	}
	result := parsed.ForRequest(Request{MaxDREDSamples: 960, SampleRate: 48000})

	window := result.FeatureWindow(0, 960, 2)
	if window.FeatureOffsetBase != -1 {
		t.Fatalf("FeatureWindow base=%d want -1", window.FeatureOffsetBase)
	}
	if window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 0 {
		t.Fatalf("FeatureWindow recoverable/missing=(%d,%d) want (0,0)", window.RecoverableFeatureFrames, window.MissingPositiveFrames)
	}

	window = result.FeatureWindow(3840, 960, 0)
	if window.FeatureOffsetBase != 5 {
		t.Fatalf("FeatureWindow base=%d want 5", window.FeatureOffsetBase)
	}
	if window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("FeatureWindow recoverable/missing=(%d,%d) want (0,2)", window.RecoverableFeatureFrames, window.MissingPositiveFrames)
	}
	got := make([]int, 2)
	if n := window.FillFeatureOffsets(got); n != len(got) {
		t.Fatalf("FillFeatureOffsets count=%d want %d", n, len(got))
	}
	want := []int{5, 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FillFeatureOffsets=%v want %v", got, want)
	}
}
