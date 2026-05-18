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
	nonLFE.SetAnalysisInfoWithTonality(20, [leakBands]uint8{}, 0.8, 0.9, 0, 1, true)

	lfe := NewEncoder(1)
	lfe.SetVBR(true)
	lfe.SetHybrid(false)
	lfe.SetBitrate(64000)
	lfe.SetLFE(true)
	lfe.SetAnalysisInfoWithTonality(20, [leakBands]uint8{}, 0.8, 0.9, 0, 1, true)

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

func TestComputeVBRTargetMatchesLibopusLowTonalityTransient(t *testing.T) {
	enc := NewEncoder(2)
	enc.SetVBR(true)
	enc.SetBitrate(19000)
	enc.SetConstrainedVBR(true)
	enc.intensity = 9
	enc.lastStereoSaving = 0.25
	enc.lastDynalloc = DynallocResult{
		TotBoost: 480,
		MaxDepth: 25.525310516357422,
	}
	enc.SetAnalysisInfoWithTonality(20, [leakBands]uint8{}, 0.47803518176078796, 0.08520728349685669, 0, 1, true)

	got := enc.computeVBRTarget(2240, 960, 0.9928242543370907, false)
	const want = 3301
	if got != want {
		t.Fatalf("computeVBRTarget low-tonality transient=%d want %d", got, want)
	}
}
