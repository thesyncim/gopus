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
	d.postfilterTest(samples, frameSize, 3, 120, 0.5, 0)
	mem := d.DecodeMem(0)
	if len(mem) != d.decodeMemHistoryLen()+240 {
		t.Fatalf("native96 decode_mem len=%d, want %d", len(mem), d.decodeMemHistoryLen()+240)
	}
	active := false
	for _, sample := range mem {
		active = active || sample != 0
	}
	if !active {
		t.Fatal("native96 postfilter did not populate decode_mem")
	}

	backing := &d.decodeMem[0]
	d.Reset()
	if len(d.DecodeMem(0)) != len(mem) {
		t.Fatal("Reset resized native96 decode_mem")
	}
	if &d.decodeMem[0] != backing {
		t.Fatal("Reset replaced native96 decode_mem instead of reusing it")
	}
	for i, sample := range d.decodeMem {
		if sample != 0 {
			t.Fatalf("Reset retained native96 decode_mem at %d: %g", i, sample)
		}
	}

	d.postfilterPeriodOld = 120
	d.postfilterPeriod = 120
	d.postfilterGainOld = 0.5
	d.postfilterGain = 0.5
	allocs := testing.AllocsPerRun(20, func() {
		d.postfilterTest(samples, frameSize, 3, 120, 0.5, 0)
	})
	if allocs != 0 {
		t.Fatalf("warmed native96 postfilter after Reset allocated %g times/call", allocs)
	}
}
