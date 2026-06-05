package celt

import "testing"

func TestApplyDelayCompensationStoresOpusResWidth(t *testing.T) {
	enc := NewEncoder(1)
	enc.delayBuffer[0] = opusRes(1.0 / 3.0)

	pcm := make([]float32, 480)
	for i := range pcm {
		pcm[i] = float32(i+1) + 1.0/3.0
	}

	got := enc.ApplyDelayCompensationScratchHybrid(pcm, 480)
	wantHead := float32(opusRes(1.0 / 3.0))
	if got[0] != wantHead {
		t.Fatalf("output[0] = %.9g, want stored opus_res %.9g", got[0], wantHead)
	}

	tailStart := len(pcm) - DelayCompensation
	for i := range DelayCompensation {
		want := opusRes(pcm[tailStart+i])
		if enc.delayBuffer[i] != want {
			t.Fatalf("delayBuffer[%d] = %.9g, want opus_res %.9g", i, enc.delayBuffer[i], want)
		}
	}
}
