package celt

import "testing"

// vbrTargetBits returns the compute_vbr target of a frame in whole bits,
// starting from the libopus base target vbr_rate-((40*C+20)<<BITRES).
func vbrTargetBits(enc *Encoder, frameSize int, tfEstimate float32, pitchChange bool, maxDepth celtGLog) int {
	lm := enc.modeConfig(frameSize).LM
	c := enc.codedChannels()
	vbrRate := int32(enc.BitrateToBits(frameSize)) << bitRes
	baseTarget := vbrRate - int32((40*c+20)<<bitRes)
	budget := enc.initFrameBudget(frameSize, lm, c, enc.payloadBudget(frameSize), &enc.scratch.rangeEncoder)
	return int(enc.computeVBR(baseTarget, lm, c, budget.equivRate, 0, tfEstimate, pitchChange, maxDepth, 0, 0) >> bitRes)
}

func TestCeltTargetBits25ms(t *testing.T) {
	frameSize := 120

	enc := NewEncoder(1)
	enc.SetBitrate(64000)
	enc.scratch.rangeEncoder.Init(make([]byte, celtPacketSizeCap))

	baseBits := enc.BitrateToBits(frameSize)
	targetBits := vbrTargetBits(enc, frameSize, 0, false, 20)

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
	nonLFE.scratch.rangeEncoder.Init(make([]byte, celtPacketSizeCap))

	lfe := NewEncoder(1)
	lfe.SetVBR(true)
	lfe.SetHybrid(false)
	lfe.SetBitrate(64000)
	lfe.SetLFE(true)
	lfe.SetAnalysisInfoWithTonality(20, [leakBands]uint8{}, 0.8, 0.9, 0, 1, true)
	lfe.scratch.rangeEncoder.Init(make([]byte, celtPacketSizeCap))

	frameSize := 960
	nonLFEBits := vbrTargetBits(nonLFE, frameSize, 0.3, false, 20)
	lfeBits := vbrTargetBits(lfe, frameSize, 0.3, false, 20)

	if lfeBits >= nonLFEBits {
		t.Fatalf("LFE target bits should be below non-LFE target bits: lfe=%d nonLFE=%d", lfeBits, nonLFEBits)
	}
}

func TestComputeTargetBitsUsesAnalysisActivityPenalty(t *testing.T) {
	frameSize := 960

	noAnalysis := NewEncoder(1)
	noAnalysis.SetVBR(true)
	noAnalysis.SetBitrate(64000)
	noAnalysis.scratch.rangeEncoder.Init(make([]byte, celtPacketSizeCap))

	withActivityPenalty := NewEncoder(1)
	withActivityPenalty.SetVBR(true)
	withActivityPenalty.SetBitrate(64000)
	withActivityPenalty.SetAnalysisInfo(20, [leakBands]uint8{}, 0.0, 0.0, 1.0, true)
	withActivityPenalty.scratch.rangeEncoder.Init(make([]byte, celtPacketSizeCap))

	bitsNoAnalysis := vbrTargetBits(noAnalysis, frameSize, 0.2, false, 20)
	bitsWithPenalty := vbrTargetBits(withActivityPenalty, frameSize, 0.2, false, 20)
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
	enc.SetAnalysisInfoWithTonality(20, [leakBands]uint8{}, 0.47803518176078796, 0.08520728349685669, 0, 1, true)

	got := enc.computeVBR(2240, 3, 2, 19000-(40*2+20)*((400>>3)-50), 480, 0.9928242543370907, false, 25.525310516357422, 0, 0)
	const want = 3301
	if got != want {
		t.Fatalf("computeVBR low-tonality transient=%d want %d", got, want)
	}
}

func TestEncoderLastTonalityUsesAnalysisFloatWidth(t *testing.T) {
	enc := NewEncoder(1)
	enc.SetLastTonality(1.0 / 3.0)
	if got, want := enc.LastTonality(), opusVal16(1.0/3.0); got != want {
		t.Fatalf("LastTonality()=%0.9g want analysis float-width %0.9g", got, want)
	}

	enc.SetLastTonality(-1)
	if got := enc.LastTonality(); got != 0 {
		t.Fatalf("LastTonality() after low clamp=%0.9g want 0", got)
	}
	enc.SetLastTonality(2)
	if got := enc.LastTonality(); got != 1 {
		t.Fatalf("LastTonality() after high clamp=%0.9g want 1", got)
	}
}

func TestCBRPayloadBytesDoesNotApplyVBR510kCap(t *testing.T) {
	tests := []struct {
		name      string
		channels  int
		bitrate   int
		frameSize int
		want      int
	}{
		{name: "stereo_10ms_packet_cap", channels: 2, bitrate: 1020000, frameSize: 480, want: 1274},
		{name: "stereo_5ms_bitrate_max", channels: 2, bitrate: 1500000, frameSize: 240, want: 937},
		{name: "mono_2_5ms_bitrate_max", channels: 1, bitrate: 1500000, frameSize: 120, want: 468},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder(tt.channels)
			enc.SetVBR(false)
			enc.SetBitrate(tt.bitrate)
			if got := enc.cbrPayloadBytes(tt.frameSize); got != tt.want {
				t.Fatalf("cbrPayloadBytes()=%d want %d", got, tt.want)
			}
		})
	}
}
