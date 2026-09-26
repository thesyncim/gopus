package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

func TestEncoderVariantSilkFinalFlushMatchesLibopusFixture(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)

	c, ok := findEncoderVariantsFixtureCase(
		encoder.ModeSILK,
		types.BandwidthWideband,
		480,
		1,
		32000,
		testsignal.EncoderVariantAMMultisineV1,
	)
	if !ok {
		t.Fatal("missing variants fixture case")
	}

	totalSamples := c.SignalFrames * c.FrameSize * c.Channels
	signal, err := testsignal.GenerateEncoderSignalVariant(c.Variant, 48000, totalSamples, c.Channels)
	if err != nil {
		t.Fatalf("generate signal: %v", err)
	}
	if hash := testsignal.HashFloat32LE(signal); hash != c.SignalSHA256 {
		t.Fatal("signal hash mismatch")
	}

	ref, err := runPairedLibopusVariantPacketReference(c, signal)
	if err != nil {
		t.Fatalf("run matched live libopus reference: %v", err)
	}
	gotPackets, gotRanges, err := encodeGopusForVariantsCase(c, signal)
	if err != nil {
		t.Fatalf("encode gopus packets: %v", err)
	}
	comparison := compareEncoderPacketRanges(ref.packets, ref.finalRanges, gotPackets, gotRanges)
	logEncoderVariantPacketReference(t, c, ref, comparison)
	if !comparison.exact() {
		t.Fatalf("SILK signal/flush packet-range parity failed: %s", comparison.summary())
	}
	final := len(ref.packets) - 1
	if final < 0 {
		t.Fatal("live reference has no packets")
	}
	t.Logf("final SILK flush frame=%d packet_bytes=%d final_range=0x%08x exact", final, len(gotPackets[final]), gotRanges[final])
}
