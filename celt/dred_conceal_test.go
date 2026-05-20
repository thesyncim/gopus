//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package celt

import "testing"

func TestUpdateStereoDREDNeuralHistoryPreservesRightShift(t *testing.T) {
	d := NewDecoder(2)
	const history = 6
	const frameSize = 2
	hist := make([]float64, history*2)
	for i := 0; i < history; i++ {
		hist[i] = float64(10 + i)
		hist[history+i] = float64(20 + i)
	}
	samples := []float64{
		100, 200,
		101, 201,
	}

	d.updateStereoDREDNeuralHistory(hist, frameSize, history, samples)

	wantL := []float64{12, 13, 14, 15, 100, 101}
	wantR := []float64{22, 23, 24, 25, 200, 201}
	for i, want := range wantL {
		if hist[i] != want {
			t.Fatalf("left[%d]=%v want %v", i, hist[i], want)
		}
	}
	for i, want := range wantR {
		if hist[history+i] != want {
			t.Fatalf("right[%d]=%v want %v", i, hist[history+i], want)
		}
	}
}
