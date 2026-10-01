package celt

import (
	"math"
	"testing"
)

func TestCoarseEnergyVariableBudgetAllocs(t *testing.T) {
	const frameSize = 120
	enc := NewEncoder(1)
	enc.SetComplexity(10)
	enc.SetVBR(false)
	enc.SetBitrate(32000)
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = float32(0.2 * math.Sin(float64(i)*0.071))
	}
	if _, err := enc.EncodeFrame(pcm, frameSize); err != nil {
		t.Fatal(err)
	}
	// Grow the packet budget on every call, including AllocsPerRun's warmup.
	// A single small-budget frame must reserve the reusable trial storage.
	bitrate := 48000
	if allocs := testing.AllocsPerRun(20, func() {
		enc.SetBitrate(bitrate)
		bitrate += 16000
		if _, err := enc.EncodeFrame(pcm, frameSize); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatalf("varying packet budgets allocate %g times per frame", allocs)
	}
}
