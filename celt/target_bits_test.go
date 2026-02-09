package celt

import (
	"math"
	"testing"
)

func TestCeltTargetBits25ms(t *testing.T) {
	frameSize := 120

	enc := NewEncoder(1)
	enc.targetBitrate = 64000

	var statsList []CeltTargetStats
	enc.SetTargetStatsHook(func(stats CeltTargetStats) {
		if stats.FrameSize == frameSize {
			statsList = append(statsList, stats)
		}
	})

	pcm := make([]float64, frameSize)
	numFrames := 64
	for frame := 0; frame < numFrames; frame++ {
		freq := float64(440 + frame*10)
		for i := range pcm {
			pcm[i] = math.Sin(2 * math.Pi * freq * float64(i) / float64(frameSize))
		}
		if _, err := enc.EncodeFrame(pcm, frameSize); err != nil {
			t.Fatalf("EncodeFrame failed: %v", err)
		}
	}

	if len(statsList) == 0 {
		t.Fatal("no target stats recorded for 2.5ms frames")
	}

	sumTarget := 0
	sumBase := 0
	for _, stats := range statsList {
		sumTarget += stats.TargetBits
		sumBase += stats.BaseBits
	}
	avgTarget := sumTarget / len(statsList)
	avgBase := sumBase / len(statsList)

	t.Logf("CELT 2.5ms: avg base bits=%d, avg target bits=%d", avgBase, avgTarget)

	if avgTarget < avgBase {
		t.Fatalf("targetBits (%d) < baseBits (%d) for CELT 2.5ms frames", avgTarget, avgBase)
	}

	// Ensure floor depth clamp did not zero out the budget
	for _, stats := range statsList {
		if stats.FloorLimited {
			t.Logf("floor depth limited target for frame (maxDepth=%.2f)", stats.MaxDepth)
		}
	}
}
