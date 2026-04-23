package dred

import "testing"

func TestDecodedInvalidateDropsFeatureVisibilityWithoutFullClear(t *testing.T) {
	var decoded Decoded
	for i := range decoded.State {
		decoded.State[i] = float32(i + 1)
	}
	for i := 0; i < 2*LatentStride; i++ {
		decoded.Latents[i] = float32(100 + i)
	}
	for i := 0; i < 2*4*NumFeatures; i++ {
		decoded.Features[i] = float32(200 + i)
	}
	decoded.NbLatents = 2

	decoded.Invalidate()
	if decoded.NbLatents != 0 {
		t.Fatalf("NbLatents after Invalidate=%d want 0", decoded.NbLatents)
	}

	var latents [2 * LatentStride]float32
	if n := decoded.FillLatents(latents[:]); n != 0 {
		t.Fatalf("FillLatents() after Invalidate=%d want 0", n)
	}
	var features [2 * 4 * NumFeatures]float32
	if n := decoded.FillFeatures(features[:]); n != 0 {
		t.Fatalf("FillFeatures() after Invalidate=%d want 0", n)
	}

	var state [StateDim]float32
	if n := decoded.FillState(state[:]); n != StateDim {
		t.Fatalf("FillState() after Invalidate=%d want %d", n, StateDim)
	}
	for i, v := range state {
		want := float32(i + 1)
		if v != want {
			t.Fatalf("state[%d]=%v want %v", i, v, want)
		}
	}
}
