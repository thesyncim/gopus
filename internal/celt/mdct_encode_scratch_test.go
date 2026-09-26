package celt

import (
	"math"
	"testing"
)

func TestMDCTForwardScratchMatchesFloat32Wrapper(t *testing.T) {
	const overlap = 120
	var scratch MDCTForwardScratch
	for _, frameSize := range []int{120, 240, 960} {
		samples := make([]float32, frameSize+overlap)
		for i := range samples {
			samples[i] = float32(math.Sin(float64(i)*0.17) + 0.3*math.Cos(float64(i)*0.031))
		}
		want := MDCTForwardWithOverlapFloat32(samples, overlap)
		got := make([]float32, frameSize)
		scratch.ForwardWithOverlapFloat32Into(samples, overlap, got)
		for i := range got {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("frameSize=%d coefficient=%d got=%08x want=%08x", frameSize, i,
					math.Float32bits(got[i]), math.Float32bits(want[i]))
			}
		}
		if allocs := testing.AllocsPerRun(20, func() {
			scratch.ForwardWithOverlapFloat32Into(samples, overlap, got)
		}); allocs != 0 {
			t.Fatalf("frameSize=%d warm allocations=%g want 0", frameSize, allocs)
		}
	}
}
