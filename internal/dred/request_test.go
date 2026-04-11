package dred

import (
	"reflect"
	"testing"
)

func TestRequestedFeatureFrames(t *testing.T) {
	for _, tc := range []struct {
		name           string
		maxDredSamples int
		sampleRate     int
		want           int
	}{
		{name: "zero_request", maxDredSamples: 0, sampleRate: 48000, want: 2},
		{name: "small_window", maxDredSamples: 960, sampleRate: 48000, want: 4},
		{name: "cap_to_max_frames", maxDredSamples: 50000, sampleRate: 48000, want: MaxFrames},
		{name: "invalid_rate", maxDredSamples: 960, sampleRate: 0, want: 0},
		{name: "negative_request", maxDredSamples: -1, sampleRate: 48000, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RequestedFeatureFrames(tc.maxDredSamples, tc.sampleRate); got != tc.want {
				t.Fatalf("RequestedFeatureFrames(%d, %d)=%d want %d", tc.maxDredSamples, tc.sampleRate, got, tc.want)
			}
		})
	}
}

func TestMaxLatentsForRequest(t *testing.T) {
	for _, tc := range []struct {
		name           string
		maxDredSamples int
		sampleRate     int
		want           int
	}{
		{name: "two_feature_frames", maxDredSamples: 0, sampleRate: 48000, want: 1},
		{name: "four_feature_frames", maxDredSamples: 960, sampleRate: 48000, want: 1},
		{name: "five_feature_frames", maxDredSamples: 1440, sampleRate: 48000, want: 2},
		{name: "cap_to_max_latents", maxDredSamples: 50000, sampleRate: 48000, want: MaxLatents},
		{name: "invalid_request", maxDredSamples: 960, sampleRate: 0, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaxLatentsForRequest(tc.maxDredSamples, tc.sampleRate); got != tc.want {
				t.Fatalf("MaxLatentsForRequest(%d, %d)=%d want %d", tc.maxDredSamples, tc.sampleRate, got, tc.want)
			}
		})
	}
}

func TestHeaderFillQuantizerLevels(t *testing.T) {
	h := Header{Q0: 6, DQ: 3, QMax: 9}
	got := make([]int, 6)
	n := h.FillQuantizerLevels(got, 10080, 48000)
	if n != len(got) {
		t.Fatalf("FillQuantizerLevels count=%d want %d", n, len(got))
	}
	want := []int{6, 6, 7, 7, 7, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FillQuantizerLevels=%v want %v", got, want)
	}
}

func TestHeaderMaxAvailableSamples(t *testing.T) {
	h := Header{DredOffset: 4}
	if got := h.MaxAvailableSamples(960, 48000); got != 1440 {
		t.Fatalf("MaxAvailableSamples=%d want 1440", got)
	}
	if got := h.MaxAvailableSamples(0, 48000); got != 1440 {
		t.Fatalf("MaxAvailableSamples(zero_request)=%d want 1440", got)
	}
	if got := (Header{DredOffset: 120}).MaxAvailableSamples(0, 48000); got != 0 {
		t.Fatalf("MaxAvailableSamples(large_offset)=%d want 0", got)
	}
}
