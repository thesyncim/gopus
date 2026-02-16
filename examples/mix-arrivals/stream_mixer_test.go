package main

import "testing"

func TestStreamMixerOutOfOrderFrames(t *testing.T) {
	t.Helper()

	m, err := NewStreamMixer(StreamMixerConfig{
		Channels:          2,
		FrameSamples:      2,
		MaxLookaheadFrame: 8,
	})
	if err != nil {
		t.Fatalf("NewStreamMixer error: %v", err)
	}
	if err := m.AddTrack("music", TrackConfig{Gain: 1}); err != nil {
		t.Fatalf("add track error: %v", err)
	}

	if err := m.PushFrame("music", 2, []float32{10, 10, 20, 20}); err != nil {
		t.Fatalf("push frame 2 error: %v", err)
	}
	if err := m.PushFrame("music", 0, []float32{1, 1, 2, 2}); err != nil {
		t.Fatalf("push frame 0 error: %v", err)
	}

	buf := make([]float32, 4)

	start, err := m.MixNext(buf)
	if err != nil {
		t.Fatalf("mix 1 error: %v", err)
	}
	if start != 0 {
		t.Fatalf("start=%d want 0", start)
	}
	assertFloat32Slice(t, buf, []float32{1, 1, 2, 2})

	start, err = m.MixNext(buf)
	if err != nil {
		t.Fatalf("mix 2 error: %v", err)
	}
	if start != 2 {
		t.Fatalf("start=%d want 2", start)
	}
	assertFloat32Slice(t, buf, []float32{10, 10, 20, 20})
}

func TestStreamMixerDropsLateFrames(t *testing.T) {
	t.Helper()

	m, err := NewStreamMixer(StreamMixerConfig{
		Channels:          1,
		FrameSamples:      2,
		MaxLookaheadFrame: 8,
	})
	if err != nil {
		t.Fatalf("NewStreamMixer error: %v", err)
	}
	if err := m.AddTrack("late", TrackConfig{Gain: 1}); err != nil {
		t.Fatalf("add track error: %v", err)
	}

	out := make([]float32, 2)
	if _, err := m.MixNext(out); err != nil {
		t.Fatalf("MixNext error: %v", err)
	}
	if m.CursorSample() != 2 {
		t.Fatalf("cursor=%d want 2", m.CursorSample())
	}

	err = m.PushFrame("late", 0, []float32{1, 2})
	if err != ErrFrameTooLate {
		t.Fatalf("err=%v want ErrFrameTooLate", err)
	}

	stats := m.Stats()
	if stats.DroppedLate != 1 {
		t.Fatalf("DroppedLate=%d want 1", stats.DroppedLate)
	}
}

func TestStreamMixerRejectsFarAheadFrames(t *testing.T) {
	t.Helper()

	m, err := NewStreamMixer(StreamMixerConfig{
		Channels:          1,
		FrameSamples:      2,
		MaxLookaheadFrame: 2, // 4 samples.
	})
	if err != nil {
		t.Fatalf("NewStreamMixer error: %v", err)
	}
	if err := m.AddTrack("voice", TrackConfig{Gain: 1}); err != nil {
		t.Fatalf("add track error: %v", err)
	}

	err = m.PushFrame("voice", 5, []float32{1, 2})
	if err != ErrFrameTooFarAhead {
		t.Fatalf("err=%v want ErrFrameTooFarAhead", err)
	}

	stats := m.Stats()
	if stats.DroppedAhead != 1 {
		t.Fatalf("DroppedAhead=%d want 1", stats.DroppedAhead)
	}
}

func TestStreamMixerGainAndMute(t *testing.T) {
	t.Helper()

	m, err := NewStreamMixer(StreamMixerConfig{
		Channels:          1,
		FrameSamples:      2,
		MaxLookaheadFrame: 8,
	})
	if err != nil {
		t.Fatalf("NewStreamMixer error: %v", err)
	}

	if err := m.AddTrack("voice", TrackConfig{Gain: 0.5}); err != nil {
		t.Fatalf("add track error: %v", err)
	}
	if err := m.PushFrame("voice", 0, []float32{2, 4}); err != nil {
		t.Fatalf("push frame error: %v", err)
	}
	out := make([]float32, 2)
	if _, err := m.MixNext(out); err != nil {
		t.Fatalf("mix error: %v", err)
	}
	assertFloat32Slice(t, out, []float32{1, 2})

	if err := m.SetTrackMuted("voice", true); err != nil {
		t.Fatalf("set muted error: %v", err)
	}
	if err := m.PushFrame("voice", 2, []float32{8, 10}); err != nil {
		t.Fatalf("push muted frame error: %v", err)
	}
	if _, err := m.MixNext(out); err != nil {
		t.Fatalf("mix muted error: %v", err)
	}
	assertFloat32Slice(t, out, []float32{0, 0})
}

func TestStreamMixerOutputBufferValidation(t *testing.T) {
	t.Helper()

	m, err := NewStreamMixer(StreamMixerConfig{
		Channels:          2,
		FrameSamples:      2,
		MaxLookaheadFrame: 8,
	})
	if err != nil {
		t.Fatalf("NewStreamMixer error: %v", err)
	}

	_, err = m.MixNext(make([]float32, 3))
	if err != ErrOutputBufferSmall {
		t.Fatalf("err=%v want ErrOutputBufferSmall", err)
	}
}

func TestStreamMixerAddTrackLifecycle(t *testing.T) {
	t.Helper()

	m, err := NewStreamMixer(StreamMixerConfig{
		Channels:          1,
		FrameSamples:      2,
		MaxLookaheadFrame: 8,
	})
	if err != nil {
		t.Fatalf("NewStreamMixer error: %v", err)
	}

	if err := m.PushFrame("voice", 0, []float32{1, 2}); err != ErrUnknownTrack {
		t.Fatalf("push err=%v want ErrUnknownTrack", err)
	}

	if err := m.AddTrack("voice", TrackConfig{Gain: 1}); err != nil {
		t.Fatalf("add track error: %v", err)
	}
	if err := m.AddTrack("voice", TrackConfig{Gain: 1}); err != ErrTrackAlreadyAdded {
		t.Fatalf("duplicate add err=%v want ErrTrackAlreadyAdded", err)
	}

	out := make([]float32, 2)
	if err := m.PushFrame("voice", 0, []float32{2, 4}); err != nil {
		t.Fatalf("push after add error: %v", err)
	}
	if _, err := m.MixNext(out); err != nil {
		t.Fatalf("mix error: %v", err)
	}
	assertFloat32Slice(t, out, []float32{2, 4})

	if removed := m.RemoveTrack("voice"); !removed {
		t.Fatalf("expected track to be removed")
	}
	if removed := m.RemoveTrack("voice"); removed {
		t.Fatalf("expected second remove to be false")
	}
	if err := m.PushFrame("voice", 2, []float32{2, 4}); err != ErrUnknownTrack {
		t.Fatalf("push removed track err=%v want ErrUnknownTrack", err)
	}
}

func TestStreamMixerCanAddTrackAtRuntime(t *testing.T) {
	t.Helper()

	m, err := NewStreamMixer(StreamMixerConfig{
		Channels:          1,
		FrameSamples:      2,
		MaxLookaheadFrame: 8,
	})
	if err != nil {
		t.Fatalf("NewStreamMixer error: %v", err)
	}
	if err := m.AddTrack("bed", TrackConfig{Gain: 1}); err != nil {
		t.Fatalf("add bed error: %v", err)
	}
	if err := m.PushFrame("bed", 0, []float32{1, 1}); err != nil {
		t.Fatalf("push bed frame 0 error: %v", err)
	}

	out := make([]float32, 2)
	if _, err := m.MixNext(out); err != nil {
		t.Fatalf("mix frame 0 error: %v", err)
	}
	assertFloat32Slice(t, out, []float32{1, 1})

	if err := m.AddTrack("speaker", TrackConfig{Gain: 0.5}); err != nil {
		t.Fatalf("add speaker error: %v", err)
	}
	if err := m.PushFrame("speaker", 2, []float32{8, 8}); err != nil {
		t.Fatalf("push speaker error: %v", err)
	}
	if err := m.PushFrame("bed", 2, []float32{2, 2}); err != nil {
		t.Fatalf("push bed frame 1 error: %v", err)
	}
	if _, err := m.MixNext(out); err != nil {
		t.Fatalf("mix frame 1 error: %v", err)
	}
	assertFloat32Slice(t, out, []float32{6, 6})
}

func assertFloat32Slice(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d len(want)=%d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%v want %v", i, got[i], want[i])
		}
	}
}
