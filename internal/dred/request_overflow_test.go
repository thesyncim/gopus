package dred

import (
	"math"
	"testing"
)

func TestRequestedFeatureFramesNoOverflow(t *testing.T) {
	wantMax := 2 * NumRedundancyFrames
	cases := []struct {
		name           string
		maxDredSamples int
		sampleRate     int
		want           int
	}{
		{"maxInt_48k", math.MaxInt, 48000, wantMax},
		{"nearOverflow_48k", math.MaxInt/50 + 1, 48000, wantMax},
		{"maxInt_maxInt", math.MaxInt, math.MaxInt, 102},
		{"normal_48k_40ms", 1920, 48000, 6},
		{"zero", 0, 48000, 2},
		{"negative", -1, 48000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RequestedFeatureFrames(tc.maxDredSamples, tc.sampleRate); got != tc.want {
				t.Fatalf("RequestedFeatureFrames(%d, %d) = %d, want %d", tc.maxDredSamples, tc.sampleRate, got, tc.want)
			}
		})
	}
}
