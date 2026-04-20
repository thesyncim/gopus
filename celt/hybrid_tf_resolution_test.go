package celt

import "testing"

func TestFillHybridTFResolution(t *testing.T) {
	t.Run("weak transient", func(t *testing.T) {
		tfRes := []int{9, 9, 9, 9}
		tfSelect := FillHybridTFResolution(tfRes, 3, true, true, true)
		if tfSelect != 0 {
			t.Fatalf("tfSelect = %d, want 0", tfSelect)
		}
		for i, want := range []int{1, 1, 1, 9} {
			if tfRes[i] != want {
				t.Fatalf("tfRes[%d] = %d, want %d", i, tfRes[i], want)
			}
		}
	})

	t.Run("low bitrate transient", func(t *testing.T) {
		tfRes := []int{9, 9, 9}
		tfSelect := FillHybridTFResolution(tfRes, len(tfRes), true, false, true)
		if tfSelect != 1 {
			t.Fatalf("tfSelect = %d, want 1", tfSelect)
		}
		for i, got := range tfRes {
			if got != 0 {
				t.Fatalf("tfRes[%d] = %d, want 0", i, got)
			}
		}
	})

	t.Run("default transient", func(t *testing.T) {
		tfRes := []int{9, 9, 9}
		tfSelect := FillHybridTFResolution(tfRes, len(tfRes), true, false, false)
		if tfSelect != 0 {
			t.Fatalf("tfSelect = %d, want 0", tfSelect)
		}
		for i, got := range tfRes {
			if got != 1 {
				t.Fatalf("tfRes[%d] = %d, want 1", i, got)
			}
		}
	})
}
