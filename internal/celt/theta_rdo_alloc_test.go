package celt

import (
	"math"
	"testing"
)

// TestStereoThetaRDOEncodeAllocs checks a warm complexity-10 stereo encoder,
// which runs the theta RDO band trials, stays allocation-free.
func TestStereoThetaRDOEncodeAllocs(t *testing.T) {
	for _, frameSize := range []int{120, 960} {
		enc := NewEncoder(2)
		enc.SetComplexity(10)
		enc.SetBitrate(128000)
		pcm := make([]float32, 2*frameSize)
		phase := 0.0
		next := func() {
			for i := 0; i < frameSize; i++ {
				v := float32(0.3*math.Sin(phase) + 0.1*math.Sin(3.7*phase))
				pcm[2*i] = v
				pcm[2*i+1] = 0.6*v + float32(0.05*math.Cos(1.3*phase))
				phase += 0.071
			}
		}
		for range 8 {
			next()
			if _, err := enc.EncodeFrame(pcm, frameSize); err != nil {
				t.Fatal(err)
			}
		}
		allocs := testing.AllocsPerRun(50, func() {
			next()
			if _, err := enc.EncodeFrame(pcm, frameSize); err != nil {
				t.Fatal(err)
			}
		})
		if allocs != 0 {
			t.Fatalf("frame %d: steady-state allocations = %g, want 0", frameSize, allocs)
		}
	}
}
