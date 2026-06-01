package testvectors

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

func TestEncoderVariantSilkFinalFlushMatchesLibopusFixture(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)

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

	wantPackets, _, err := decodeEncoderVariantsFixturePackets(c)
	if err != nil {
		t.Fatalf("decode fixture packets: %v", err)
	}
	gotPackets, err := encodeGopusForVariantsCase(c, signal)
	if err != nil {
		t.Fatalf("encode gopus packets: %v", err)
	}
	if len(gotPackets) != len(wantPackets) {
		t.Fatalf("packet count mismatch: got=%d want=%d", len(gotPackets), len(wantPackets))
	}
	final := len(wantPackets) - 1
	if final < 0 {
		t.Fatal("fixture has no packets")
	}
	if !bytes.Equal(gotPackets[final], wantPackets[final]) {
		t.Fatalf("final flush packet mismatch at frame %d", final)
	}
}
