package celt

import "testing"

func TestUpdateHybridPrefilterHistoryUpdatesStateCadence(t *testing.T) {
	enc := NewEncoder(1)
	enc.tapsetDecision = 2
	enc.prefilterPeriod = 0
	enc.prefilterGain = 0.3
	enc.prefilterTapset = 1

	frameSize := 480
	preemph := make([]float64, frameSize)
	for i := range preemph {
		preemph[i] = float64(i+1) / 1000.0
	}

	enc.UpdateHybridPrefilterHistory(preemph, frameSize)

	if enc.prefilterPeriod != combFilterMinPeriod {
		t.Fatalf("prefilterPeriod=%d, want %d", enc.prefilterPeriod, combFilterMinPeriod)
	}
	if enc.prefilterGain != 0 {
		t.Fatalf("prefilterGain=%f, want 0", enc.prefilterGain)
	}
	if enc.prefilterTapset != enc.tapsetDecision {
		t.Fatalf("prefilterTapset=%d, want %d", enc.prefilterTapset, enc.tapsetDecision)
	}
}
