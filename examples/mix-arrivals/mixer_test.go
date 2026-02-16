package main

import "testing"

func TestMixTimedTracksAppliesOffsetsAndGains(t *testing.T) {
	t.Helper()

	tracks := []TimedTrack{
		{
			Name:        "a",
			StartSample: 0,
			Gain:        1.0,
			PCM:         []float32{1, 1, 2, 2},
		},
		{
			Name:        "b",
			StartSample: 1,
			Gain:        0.5,
			PCM:         []float32{4, 4, 6, 6},
		},
	}

	got, err := MixTimedTracks(tracks, 2)
	if err != nil {
		t.Fatalf("MixTimedTracks returned error: %v", err)
	}

	want := []float32{
		1, 1,
		4, 4,
		3, 3,
	}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d len(want)=%d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%v want %v", i, got[i], want[i])
		}
	}
}

func TestMixTimedTracksRejectsUnalignedPCM(t *testing.T) {
	t.Helper()

	_, err := MixTimedTracks([]TimedTrack{
		{
			Name:        "bad",
			StartSample: 0,
			Gain:        1,
			PCM:         []float32{1, 2, 3},
		},
	}, 2)
	if err == nil {
		t.Fatalf("expected error for unaligned PCM, got nil")
	}
}

func TestNormalizePeakInPlaceScalesToTarget(t *testing.T) {
	t.Helper()

	samples := []float32{0.25, -2.0, 1.0}
	peak, gain := NormalizePeakInPlace(samples, 1.0)
	if peak != 2.0 {
		t.Fatalf("peak=%v want 2.0", peak)
	}
	if gain != 0.5 {
		t.Fatalf("gain=%v want 0.5", gain)
	}

	want := []float32{0.125, -1.0, 0.5}
	for i := range want {
		if samples[i] != want[i] {
			t.Fatalf("sample[%d]=%v want %v", i, samples[i], want[i])
		}
	}
}
