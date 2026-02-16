package main

import "testing"

func TestMixTimedTracksWebRTCWithNetwork_NoLossMatchesOffline(t *testing.T) {
	t.Helper()

	tracks := []TimedTrack{
		{
			Name:        "a",
			StartSample: 0,
			Gain:        1.0,
			PCM: []float32{
				1, 1, 2, 2,
				3, 3, 4, 4,
			},
		},
		{
			Name:        "b",
			StartSample: 1,
			Gain:        0.5,
			PCM: []float32{
				10, 10, 20, 20,
				30, 30, 40, 40,
			},
		},
	}

	offline, err := MixTimedTracks(tracks, channels)
	if err != nil {
		t.Fatalf("offline mix error: %v", err)
	}

	cfg := DefaultNetworkSimConfig()
	cfg.BaseLossProbability = 0
	cfg.BurstStartProbability = 0
	cfg.BurstContinueProbability = 0
	cfg.MaxNegativeJitterFrames = 0
	cfg.MaxPositiveJitterFrames = 0
	cfg.EnablePLC = false

	got, streamStats, netStats, err := MixTimedTracksWebRTCWithNetwork(tracks, 2, cfg)
	if err != nil {
		t.Fatalf("network mix error: %v", err)
	}

	if streamStats.DroppedLate != 0 {
		t.Fatalf("DroppedLate=%d want 0", streamStats.DroppedLate)
	}
	if streamStats.DroppedAhead != 0 {
		t.Fatalf("DroppedAhead=%d want 0", streamStats.DroppedAhead)
	}
	if netStats.DroppedByNetwork != 0 {
		t.Fatalf("DroppedByNetwork=%d want 0", netStats.DroppedByNetwork)
	}
	if netStats.ConcealedFrames != 0 {
		t.Fatalf("ConcealedFrames=%d want 0", netStats.ConcealedFrames)
	}
	assertFloat32Slice(t, got, offline)
}

func TestMixTimedTracksWebRTCWithNetwork_AllLossNoPLCIsSilence(t *testing.T) {
	t.Helper()

	tracks := []TimedTrack{
		{
			Name:        "a",
			StartSample: 0,
			Gain:        1.0,
			PCM: []float32{
				1, 1, 2, 2,
				3, 3, 4, 4,
			},
		},
	}

	cfg := DefaultNetworkSimConfig()
	cfg.BaseLossProbability = 1
	cfg.BurstStartProbability = 0
	cfg.BurstContinueProbability = 0
	cfg.MaxNegativeJitterFrames = 0
	cfg.MaxPositiveJitterFrames = 0
	cfg.EnablePLC = false

	got, _, netStats, err := MixTimedTracksWebRTCWithNetwork(tracks, 2, cfg)
	if err != nil {
		t.Fatalf("network mix error: %v", err)
	}

	if netStats.GeneratedFrames == 0 {
		t.Fatalf("GeneratedFrames=0 want >0")
	}
	if netStats.DroppedByNetwork != netStats.GeneratedFrames {
		t.Fatalf("DroppedByNetwork=%d want %d", netStats.DroppedByNetwork, netStats.GeneratedFrames)
	}
	for i := range got {
		if got[i] != 0 {
			t.Fatalf("sample[%d]=%v want 0", i, got[i])
		}
	}
}
