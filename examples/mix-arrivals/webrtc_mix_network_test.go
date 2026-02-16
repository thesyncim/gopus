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

func TestDefaultNetworkSimConfig_ProducesModerateLossRate(t *testing.T) {
	t.Helper()

	cfg := DefaultNetworkSimConfig()
	state := newTrackNetworkState("speaker-a", cfg.Seed)

	const frames = 100000
	dropped := 0
	for i := 0; i < frames; i++ {
		if shouldDropFrame(&state, cfg) {
			dropped++
		}
	}

	rate := float64(dropped) / frames
	if rate < 0.005 {
		t.Fatalf("drop rate too low: %.4f", rate)
	}
	if rate > 0.08 {
		t.Fatalf("drop rate too high: %.4f", rate)
	}
}

func TestConcealFromLastGood_DecaysAcrossRuns(t *testing.T) {
	t.Helper()

	lastGood := []float32{1, 1, 1, 1, 1, 1}
	state := uint32(123)

	first := concealFromLastGood(lastGood, 0.8, 0.05, 0, 1, &state)
	second := concealFromLastGood(lastGood, 0.8, 0.05, 0, 2, &state)

	if avgAbs(second) >= avgAbs(first) {
		t.Fatalf("concealment did not decay: first=%f second=%f", avgAbs(first), avgAbs(second))
	}
	if first[len(first)-1] >= first[0] {
		t.Fatalf("expected intra-frame fade down, got start=%f end=%f", first[0], first[len(first)-1])
	}
}

func avgAbs(samples []float32) float32 {
	if len(samples) == 0 {
		return 0
	}
	var sum float32
	for i := range samples {
		if samples[i] < 0 {
			sum -= samples[i]
		} else {
			sum += samples[i]
		}
	}
	return sum / float32(len(samples))
}
