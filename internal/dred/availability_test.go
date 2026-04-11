package dred

import "testing"

func TestHeaderAvailability(t *testing.T) {
	h := Header{Q0: 6, DQ: 3, QMax: 15, DredOffset: 4}
	got := h.Availability(960, 48000)
	if got.FeatureFrames != 4 {
		t.Fatalf("FeatureFrames=%d want 4", got.FeatureFrames)
	}
	if got.MaxLatents != 1 {
		t.Fatalf("MaxLatents=%d want 1", got.MaxLatents)
	}
	if got.OffsetSamples != 480 {
		t.Fatalf("OffsetSamples=%d want 480", got.OffsetSamples)
	}
	if got.EndSamples != 0 {
		t.Fatalf("EndSamples=%d want 0", got.EndSamples)
	}
	if got.AvailableSamples != 1440 {
		t.Fatalf("AvailableSamples=%d want 1440", got.AvailableSamples)
	}
}

func TestHeaderAvailabilityClampsNegative(t *testing.T) {
	h := Header{DredOffset: 120}
	got := h.Availability(0, 48000)
	if got.FeatureFrames != 2 {
		t.Fatalf("FeatureFrames=%d want 2", got.FeatureFrames)
	}
	if got.MaxLatents != 1 {
		t.Fatalf("MaxLatents=%d want 1", got.MaxLatents)
	}
	if got.OffsetSamples != 14400 {
		t.Fatalf("OffsetSamples=%d want 14400", got.OffsetSamples)
	}
	if got.EndSamples != 0 {
		t.Fatalf("EndSamples=%d want 0", got.EndSamples)
	}
	if got.AvailableSamples != 0 {
		t.Fatalf("AvailableSamples=%d want 0", got.AvailableSamples)
	}
}
