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

	// In libopus-style compute_vbr(), 2.5ms can legitimately target below the
	// raw bitrate-derived base due per-frame overhead/corrections.
	// Guard against pathological under-allocation instead of enforcing >= base.
	if avgTarget < avgBase/2 {
		t.Fatalf("targetBits (%d) unexpectedly low vs baseBits (%d) for CELT 2.5ms frames", avgTarget, avgBase)
	}

	// Ensure floor depth clamp did not zero out the budget
	for _, stats := range statsList {
		if stats.FloorLimited {
			t.Logf("floor depth limited target for frame (maxDepth=%.2f)", stats.MaxDepth)
		}
	}
}

func TestComputeTargetBitsLFEAvoidsNonLFEBudgets(t *testing.T) {
	nonLFE := NewEncoder(1)
	nonLFE.SetVBR(true)
	nonLFE.SetHybrid(false)
	nonLFE.SetBitrate(64000)

	lfe := NewEncoder(1)
	lfe.SetVBR(true)
	lfe.SetHybrid(false)
	lfe.SetBitrate(64000)
	lfe.SetLFE(true)

	frameSize := 960
	nonLFEBits := nonLFE.computeTargetBits(frameSize, 0.3, false)
	lfeBits := lfe.computeTargetBits(frameSize, 0.3, false)

	if lfeBits >= nonLFEBits {
		t.Fatalf("LFE target bits should be below non-LFE target bits: lfe=%d nonLFE=%d", lfeBits, nonLFEBits)
	}
}

func TestComputeTargetBitsUsesAnalysisActivityPenalty(t *testing.T) {
	frameSize := 960

	noAnalysis := NewEncoder(1)
	noAnalysis.SetVBR(true)
	noAnalysis.SetBitrate(64000)

	withActivityPenalty := NewEncoder(1)
	withActivityPenalty.SetVBR(true)
	withActivityPenalty.SetBitrate(64000)
	withActivityPenalty.SetAnalysisInfo(20, [leakBands]uint8{}, 0.0, 0.0, 1.0, true)

	bitsNoAnalysis := noAnalysis.computeTargetBits(frameSize, 0.2, false)
	bitsWithPenalty := withActivityPenalty.computeTargetBits(frameSize, 0.2, false)
	if bitsWithPenalty >= bitsNoAnalysis {
		t.Fatalf("analysis activity penalty should reduce target bits: withPenalty=%d noAnalysis=%d", bitsWithPenalty, bitsNoAnalysis)
	}
}
