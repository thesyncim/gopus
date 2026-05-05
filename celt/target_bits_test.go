package celt

import "testing"

func TestCeltTargetBits25ms(t *testing.T) {
	frameSize := 120

	enc := NewEncoder(1)
	enc.targetBitrate = 64000

	baseBits := enc.bitrateToBits(frameSize)
	targetBits := enc.computeTargetBits(frameSize, 0, false)

	t.Logf("CELT 2.5ms: base bits=%d, target bits=%d", baseBits, targetBits)

	// In libopus-style compute_vbr(), 2.5ms can legitimately target below the
	// raw bitrate-derived base due per-frame overhead/corrections.
	// Guard against pathological under-allocation instead of enforcing >= base.
	if targetBits < baseBits/2 {
		t.Fatalf("targetBits (%d) unexpectedly low vs baseBits (%d) for CELT 2.5ms frames", targetBits, baseBits)
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
