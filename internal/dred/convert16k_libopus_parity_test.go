//go:build gopus_dred || gopus_extra_controls

package dred

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestConvertTo16kMonoFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name              string
		sampleRate        int
		channels          int
		frameSamplesPerCh int
	}{
		{name: "8k mono 20ms", sampleRate: 8000, channels: 1, frameSamplesPerCh: 160},
		{name: "8k stereo 20ms", sampleRate: 8000, channels: 2, frameSamplesPerCh: 160},
		{name: "12k mono 20ms", sampleRate: 12000, channels: 1, frameSamplesPerCh: 240},
		{name: "12k stereo 20ms", sampleRate: 12000, channels: 2, frameSamplesPerCh: 240},
		{name: "16k mono 20ms", sampleRate: 16000, channels: 1, frameSamplesPerCh: 320},
		{name: "16k stereo 20ms", sampleRate: 16000, channels: 2, frameSamplesPerCh: 320},
		{name: "24k mono 20ms", sampleRate: 24000, channels: 1, frameSamplesPerCh: 480},
		{name: "24k stereo 20ms", sampleRate: 24000, channels: 2, frameSamplesPerCh: 480},
		{name: "48k mono 20ms", sampleRate: 48000, channels: 1, frameSamplesPerCh: 960},
		{name: "48k stereo 20ms", sampleRate: 48000, channels: 2, frameSamplesPerCh: 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in32 := makeDREDConvertSignal(tc.sampleRate, tc.channels, tc.frameSamplesPerCh)

			want, wantMem, err := probeLibopusDREDConvert16k(tc.sampleRate, tc.channels, [ResamplingOrder + 1]float32{}, in32)
			if err != nil {
				libopustest.HelperUnavailable(t, "convert", err)
			}

			var gotMem [ResamplingOrder + 1]float32
			got := make([]float32, len(want))
			n := ConvertTo16kMonoFloat32(got, &gotMem, in32, tc.sampleRate, tc.channels)
			if n != len(want) {
				t.Fatalf("ConvertTo16kMonoFloat32 length=%d want %d", n, len(want))
			}
			assertFloat32SliceBits(t, got[:n], want, "output")
			assertFloat32SliceBits(t, gotMem[:], wantMem[:], "mem")
		})
	}
}

func TestConvertTo16kMonoFloat32MatchesLibopusAcrossCalls(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name              string
		sampleRate        int
		channels          int
		frameSamplesPerCh int
	}{
		{name: "8k mono 10ms", sampleRate: 8000, channels: 1, frameSamplesPerCh: 80},
		{name: "8k stereo 10ms", sampleRate: 8000, channels: 2, frameSamplesPerCh: 80},
		{name: "12k mono 10ms", sampleRate: 12000, channels: 1, frameSamplesPerCh: 120},
		{name: "12k stereo 10ms", sampleRate: 12000, channels: 2, frameSamplesPerCh: 120},
		{name: "24k mono 10ms", sampleRate: 24000, channels: 1, frameSamplesPerCh: 240},
		{name: "24k stereo 10ms", sampleRate: 24000, channels: 2, frameSamplesPerCh: 240},
		{name: "48k mono 10ms", sampleRate: 48000, channels: 1, frameSamplesPerCh: 480},
		{name: "48k stereo 10ms", sampleRate: 48000, channels: 2, frameSamplesPerCh: 480},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first32 := makeDREDConvertSignal(tc.sampleRate, tc.channels, tc.frameSamplesPerCh)
			second32 := makeDREDConvertSignal(tc.sampleRate+137, tc.channels, tc.frameSamplesPerCh)

			var helperMem [ResamplingOrder + 1]float32
			wantFirst, helperMem, err := probeLibopusDREDConvert16k(tc.sampleRate, tc.channels, helperMem, first32)
			if err != nil {
				libopustest.HelperUnavailable(t, "convert", err)
			}
			wantSecond, helperMem, err := probeLibopusDREDConvert16k(tc.sampleRate, tc.channels, helperMem, second32)
			if err != nil {
				t.Fatalf("second libopus convert call failed: %v", err)
			}

			var gotMem [ResamplingOrder + 1]float32
			gotFirst := make([]float32, len(wantFirst))
			nFirst := ConvertTo16kMonoFloat32(gotFirst, &gotMem, first32, tc.sampleRate, tc.channels)
			if nFirst != len(wantFirst) {
				t.Fatalf("first ConvertTo16kMonoFloat32 length=%d want %d", nFirst, len(wantFirst))
			}
			assertFloat32SliceBits(t, gotFirst[:nFirst], wantFirst, "first output")

			gotSecond := make([]float32, len(wantSecond))
			nSecond := ConvertTo16kMonoFloat32(gotSecond, &gotMem, second32, tc.sampleRate, tc.channels)
			if nSecond != len(wantSecond) {
				t.Fatalf("second ConvertTo16kMonoFloat32 length=%d want %d", nSecond, len(wantSecond))
			}
			assertFloat32SliceBits(t, gotSecond[:nSecond], wantSecond, "second output")
			assertFloat32SliceBits(t, gotMem[:], helperMem[:], "final mem")
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

func assertFloat32SliceBits(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%08x %v want %08x %v",
				label, i, math.Float32bits(got[i]), got[i], math.Float32bits(want[i]), want[i])
		}
	}
}
