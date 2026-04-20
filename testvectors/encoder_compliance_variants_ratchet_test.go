package testvectors

import "testing"

func TestEncoderVariantRatchetGapFloorClampsPositiveStretchTargets(t *testing.T) {
	if got := encoderVariantRatchetGapFloor(12.5); got != 0 {
		t.Fatalf("positive stretch floor should clamp to parity: got %.2f want 0", got)
	}
	if got := encoderVariantRatchetGapFloor(-0.5); got != -0.5 {
		t.Fatalf("negative floor should be preserved: got %.2f want -0.50", got)
	}
}

func TestEncoderVariantRatchetMissesTreatParityAsPassingAgainstPositiveBaseline(t *testing.T) {
	baseline := encoderVariantsBaselineTC{
		MinGapQ:          7.5,
		MaxMeanAbsPacket: 0,
		MaxP95AbsPacket:  0,
		MaxModeMismatch:  0.02,
		MaxHistogramL1:   0.02,
	}
	stats := encoderPacketProfileStats{}

	if misses := encoderVariantRatchetMisses(baseline, 0, stats); len(misses) != 0 {
		t.Fatalf("gap at parity should not miss a positive ratchet floor: %v", misses)
	}
	if misses := encoderVariantRatchetMisses(baseline, -0.04, stats); len(misses) != 0 {
		t.Fatalf("small negative measurement noise should stay within tolerance: %v", misses)
	}

	misses := encoderVariantRatchetMisses(baseline, -0.25, stats)
	if len(misses) != 1 || misses[0] != "gap -0.25 < 0.00" {
		t.Fatalf("unexpected misses: %v", misses)
	}
}

func TestBuildBaselineCaseCapsPositiveGapFloorAtParity(t *testing.T) {
	got := buildBaselineCase(
		encoderComplianceVariantsFixtureCase{
			Name:    "SILK-WB-20ms-stereo-48k",
			Variant: "am_multisine_v1",
			Mode:    "silk",
		},
		8.0,
		encoderPacketProfileStats{},
	)
	if got.MinGapQ != 0 {
		t.Fatalf("buildBaselineCase MinGapQ=%.2f want 0", got.MinGapQ)
	}
}
