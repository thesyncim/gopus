//go:build gopus_qext

package celt

import (
	"math"
	"testing"
)

func TestNative96ResetClearsActivePostfilterHistory(t *testing.T) {
	d := NewDecoder(1)
	d.EnableHD96kMode()
	d.postfilterPeriodOld = 120
	d.postfilterPeriod = 120
	d.postfilterGainOld = 0.5
	d.postfilterGain = 0.5

	const frameSize = 1920
	samples := make([]float32, frameSize)
	for i := range samples {
		samples[i] = float32(0.3 * math.Sin(2*math.Pi*330*float64(i)/96000))
	}
	d.applyHD96kPostfilterInterleaved(samples, frameSize, 3, 120, 0.5, 0)
	state := d.qextState()
	if state == nil {
		t.Fatal("native96 postfilter state was not created")
	}
	if len(state.hd96kPostMem) != hd96kCombHistory {
		t.Fatalf("native96 postfilter history len=%d, want %d", len(state.hd96kPostMem), hd96kCombHistory)
	}
	active := false
	for _, sample := range state.hd96kPostMem {
		active = active || sample != 0
	}
	if !active {
		t.Fatal("native96 postfilter did not populate persistent history")
	}

	history := state.hd96kPostMem
	d.Reset()
	state = d.qextState()
	if state == nil || len(state.hd96kPostMem) != len(history) {
		t.Fatal("Reset discarded native96 QEXT postfilter scratch")
	}
	if &state.hd96kPostMem[0] != &history[0] {
		t.Fatal("Reset replaced native96 QEXT postfilter history instead of reusing it")
	}
	for i, sample := range state.hd96kPostMem {
		if sample != 0 {
			t.Fatalf("Reset retained native96 postfilter history at %d: %g", i, sample)
		}
	}

	d.postfilterPeriodOld = 120
	d.postfilterPeriod = 120
	d.postfilterGainOld = 0.5
	d.postfilterGain = 0.5
	allocs := testing.AllocsPerRun(20, func() {
		d.applyHD96kPostfilterInterleaved(samples, frameSize, 3, 120, 0.5, 0)
	})
	if allocs != 0 {
		t.Fatalf("warmed native96 postfilter after Reset allocated %g times/call", allocs)
	}
}
