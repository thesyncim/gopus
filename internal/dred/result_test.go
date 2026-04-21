package dred

import (
	"reflect"
	"testing"
)

func TestParsedForRequest(t *testing.T) {
	payload := makeHeaderPayloadForTest(t, 6, 3, 9, 0, 8, -4)
	parsed, err := ParsePayload(payload, 8)
	if err != nil {
		t.Fatalf("ParsePayload error: %v", err)
	}

	result := parsed.ForRequest(Request{MaxDREDSamples: 5760, SampleRate: 48000})
	if result.Availability.FeatureFrames != 14 {
		t.Fatalf("FeatureFrames=%d want 14", result.Availability.FeatureFrames)
	}
	if result.Availability.MaxLatents != 0 {
		t.Fatalf("MaxLatents=%d want 0", result.Availability.MaxLatents)
	}
	if result.MaxAvailableSamples() != 480 {
		t.Fatalf("MaxAvailableSamples=%d want 480", result.MaxAvailableSamples())
	}

	quant := make([]int, 4)
	if n := result.FillQuantizerLevels(quant); n != 0 {
		t.Fatalf("FillQuantizerLevels count=%d want 0", n)
	}
	want := []int{0, 0, 0, 0}
	if !reflect.DeepEqual(quant, want) {
		t.Fatalf("FillQuantizerLevels=%v want %v", quant, want)
	}
}
