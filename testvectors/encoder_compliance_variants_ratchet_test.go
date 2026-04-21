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

func TestEncoderVariantThresholdForArchAppliesAMD64Overrides(t *testing.T) {
	cases := []struct {
		name string
		tc   encoderComplianceVariantsFixtureCase
		arch string
		want float64
	}{
		{
			name: "amd64 celt shortframe override",
			tc: encoderComplianceVariantsFixtureCase{
				Name:    "CELT-FB-2.5ms-mono-64k",
				Variant: "chirp_sweep_v1",
				Mode:    "celt",
			},
			arch: "amd64",
			want: -150.0,
		},
		{
			name: "amd64 hybrid stereo override",
			tc: encoderComplianceVariantsFixtureCase{
				Name:     "HYBRID-FB-20ms-stereo-96k",
				Variant:  "am_multisine_v1",
				Mode:     "hybrid",
				Channels: 2,
			},
			arch: "amd64",
			want: -10.0,
		},
		{
			name: "arm64 keeps default hybrid floor",
			tc: encoderComplianceVariantsFixtureCase{
				Name:     "HYBRID-FB-20ms-stereo-96k",
				Variant:  "am_multisine_v1",
				Mode:     "hybrid",
				Channels: 2,
			},
			arch: "arm64",
			want: -6.0,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := encoderVariantThresholdForArch(tc.tc, tc.arch)
			if got.minGapQ != tc.want {
				t.Fatalf("minGapQ=%.2f want %.2f", got.minGapQ, tc.want)
			}
		})
	}
}
