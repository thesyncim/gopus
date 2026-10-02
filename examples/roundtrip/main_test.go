package main

import (
	"math"
	"testing"
)

func TestGenerateSignalValidatesChannelsAndDuration(t *testing.T) {
	maxIntDuration := float64(int(^uint(0)>>1)) / float64(sampleRate)
	for _, tt := range []struct {
		name     string
		duration float64
		channels int
		signal   string
	}{
		{name: "negative channels", duration: 1, channels: -1},
		{name: "unsupported channels", duration: 1, channels: 3},
		{name: "negative duration", duration: -1, channels: 2},
		{name: "not a number", duration: math.NaN(), channels: 2},
		{name: "infinite duration", duration: math.Inf(1), channels: 2},
		{name: "finite but unrepresentable PCM", duration: maxIntDuration, channels: 2},
		{name: "unrepresentable PCM", duration: math.MaxFloat64, channels: 2},
		{name: "shorter than one sample", duration: 1e-9, channels: 2},
		{name: "unknown signal", duration: 1, channels: 2, signal: "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			signal := "sine"
			if tt.signal != "" {
				signal = tt.signal
			}
			pcm, err := generateSignal(signal, tt.duration, tt.channels)
			if err == nil {
				t.Fatalf("generateSignal(%q, %g, %d) succeeded", signal, tt.duration, tt.channels)
			}
			if pcm != nil {
				t.Fatalf("generateSignal returned %d samples with error %v", len(pcm), err)
			}
		})
	}
}

func TestGenerateSignalPreservesTruncatedSampleCount(t *testing.T) {
	const duration = 0.105019
	pcm, err := generateSignal("sine", duration, 2)
	if err != nil {
		t.Fatalf("generateSignal: %v", err)
	}
	const wantSamples = 5040 * 2
	if len(pcm) != wantSamples {
		t.Fatalf("PCM length = %d, want %d interleaved samples", len(pcm), wantSamples)
	}
}

func TestRunAllTestsRejectsInvalidDuration(t *testing.T) {
	if err := runAllTests(math.NaN()); err == nil {
		t.Fatal("runAllTests accepted a non-finite duration")
	}
}
