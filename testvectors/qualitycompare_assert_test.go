package testvectors

import "testing"

// AssertQuality fails t if cmp does not clear bar, logging the trusted basis and
// the measured metrics. This is the single assertion all migrated quality-parity
// tests should use, so the bar (and its libopus-anchored rationale) lives in one
// place (qualitycompare.go) rather than scattered per-test constants.
func AssertQuality(t *testing.T, cmp QualityComparison, bar QualityBar, label string) {
	t.Helper()
	t.Logf("%s: Q=%.2f delay=%d corr=%.6f rms=%.4f (bar: %s, minQ=%.1f)",
		label, cmp.Q, cmp.BestDelay, cmp.Corr, cmp.RMSRatio, bar.Desc, bar.MinQ)
	if fails := bar.Check(cmp); len(fails) > 0 {
		t.Fatalf("%s quality below libopus parity bar [%s]: %v", label, bar.Desc, fails)
	}
}

// TestUnifiedQualityComparatorSmoke verifies the canonical comparator end-to-end:
// gopus-decoded vs libopus-decoded for a hybrid matrix case must clear the
// near-exact bar (the bar that replaced the former minQ:0.0 hybrid gate).
func TestUnifiedQualityComparatorSmoke(t *testing.T) {
	requireTestTier(t, testTierParity)
	fixture, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		t.Skipf("decoder matrix fixture unavailable: %v", err)
	}
	c, ok := findDecoderMatrixCaseByName(fixture, "hybrid-fb-20ms-mono-24k")
	if !ok {
		t.Skip("hybrid-fb-20ms-mono-24k case absent from fixture")
	}
	packets, err := decodeLibopusDecoderMatrixPackets(c)
	if err != nil {
		t.Skipf("libopus matrix packets unavailable: %v", err)
	}
	ref, err := decodeLibopusDecoderMatrixSamples(c)
	if err != nil {
		t.Skipf("libopus matrix samples unavailable: %v", err)
	}
	got := decodeWithInternalDecoder(t, packets, c.Channels)

	cmp, err := CompareDecodedFloat32(got, ref, 48000, c.Channels, 240)
	if err != nil {
		t.Fatalf("CompareDecodedFloat32: %v", err)
	}
	AssertQuality(t, cmp, QualityBarForMode("hybrid", c.Channels), "unified-comparator hybrid-fb-20ms-mono-24k")
}
