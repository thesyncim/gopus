//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package dred

import (
	"math"
	"testing"
)

func TestConvertTo16kMonoFloat64MatchesLibopus(t *testing.T) {
	tests := []struct {
		name              string
		sampleRate        int
		channels          int
		frameSamplesPerCh int
	}{
		{name: "8k mono 20ms", sampleRate: 8000, channels: 1, frameSamplesPerCh: 160},
		{name: "12k mono 20ms", sampleRate: 12000, channels: 1, frameSamplesPerCh: 240},
		{name: "16k mono 20ms", sampleRate: 16000, channels: 1, frameSamplesPerCh: 320},
		{name: "24k mono 20ms", sampleRate: 24000, channels: 1, frameSamplesPerCh: 480},
		{name: "48k mono 20ms", sampleRate: 48000, channels: 1, frameSamplesPerCh: 960},
		{name: "48k stereo 20ms", sampleRate: 48000, channels: 2, frameSamplesPerCh: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in32 := makeDREDConvertSignal(tc.sampleRate, tc.channels, tc.frameSamplesPerCh)
			in64 := float32To64Slice(in32)

			want, wantMem, err := probeLibopusDREDConvert16k(tc.sampleRate, tc.channels, [ResamplingOrder + 1]float32{}, in32)
			if err != nil {
				t.Skipf("libopus convert helper unavailable: %v", err)
			}

			var gotMem [ResamplingOrder + 1]float32
			got := make([]float32, len(want))
			n := ConvertTo16kMonoFloat64(got, &gotMem, in64, tc.sampleRate, tc.channels)
			if n != len(want) {
				t.Fatalf("ConvertTo16kMonoFloat64 length=%d want %d", n, len(want))
			}
			assertFloat32SliceNear(t, got[:n], want, 2e-6, "output")
			assertFloat32SliceNear(t, gotMem[:], wantMem[:], 2e-6, "mem")
		})
	}
}

func TestConvertTo16kMonoFloat64MatchesLibopusAcrossCalls(t *testing.T) {
	tests := []struct {
		name              string
		sampleRate        int
		channels          int
		frameSamplesPerCh int
	}{
		{name: "48k mono 10ms", sampleRate: 48000, channels: 1, frameSamplesPerCh: 480},
		{name: "12k mono 10ms", sampleRate: 12000, channels: 1, frameSamplesPerCh: 120},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first32 := makeDREDConvertSignal(tc.sampleRate, tc.channels, tc.frameSamplesPerCh)
			second32 := makeDREDConvertSignal(tc.sampleRate+137, tc.channels, tc.frameSamplesPerCh)
			first64 := float32To64Slice(first32)
			second64 := float32To64Slice(second32)

			var helperMem [ResamplingOrder + 1]float32
			wantFirst, helperMem, err := probeLibopusDREDConvert16k(tc.sampleRate, tc.channels, helperMem, first32)
			if err != nil {
				t.Skipf("libopus convert helper unavailable: %v", err)
			}
			wantSecond, helperMem, err := probeLibopusDREDConvert16k(tc.sampleRate, tc.channels, helperMem, second32)
			if err != nil {
				t.Fatalf("second libopus convert call failed: %v", err)
			}

			var gotMem [ResamplingOrder + 1]float32
			gotFirst := make([]float32, len(wantFirst))
			nFirst := ConvertTo16kMonoFloat64(gotFirst, &gotMem, first64, tc.sampleRate, tc.channels)
			if nFirst != len(wantFirst) {
				t.Fatalf("first ConvertTo16kMonoFloat64 length=%d want %d", nFirst, len(wantFirst))
			}
			assertFloat32SliceNear(t, gotFirst[:nFirst], wantFirst, 2e-6, "first output")

			gotSecond := make([]float32, len(wantSecond))
			nSecond := ConvertTo16kMonoFloat64(gotSecond, &gotMem, second64, tc.sampleRate, tc.channels)
			if nSecond != len(wantSecond) {
				t.Fatalf("second ConvertTo16kMonoFloat64 length=%d want %d", nSecond, len(wantSecond))
			}
			assertFloat32SliceNear(t, gotSecond[:nSecond], wantSecond, 2e-6, "second output")
			assertFloat32SliceNear(t, gotMem[:], helperMem[:], 2e-6, "final mem")
		})
	}
}

func makeDREDConvertSignal(seed, channels, frameSamplesPerCh int) []float32 {
	total := frameSamplesPerCh * channels
	out := make([]float32, total)
	baseRate := float64(8000 + (seed % 4000))
	for i := 0; i < frameSamplesPerCh; i++ {
		t := float64(i) / baseRate
		mono := float32(0.2*math.Sin(2*math.Pi*173*t) + 0.07*math.Sin(2*math.Pi*611*t+0.3))
		if channels == 1 {
			out[i] = mono
			continue
		}
		out[2*i] = mono + 0.03*float32(math.Sin(2*math.Pi*257*t+0.1))
		out[2*i+1] = -mono + 0.05*float32(math.Sin(2*math.Pi*389*t+0.2))
	}
	return out
}

func float32To64Slice(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

func assertFloat32SliceNear(t *testing.T, got, want []float32, tol float64, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		diff := math.Abs(float64(got[i] - want[i]))
		if diff > tol {
			t.Fatalf("%s[%d]=%v want %v (diff=%g > %g)", label, i, got[i], want[i], diff, tol)
		}
	}
}
