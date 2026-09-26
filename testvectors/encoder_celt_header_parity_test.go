package testvectors

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/testsignal"
)

type celtPostfilterHeader struct {
	period int
	qg     int
	tapset int
}

func TestEncoderVariantCELTHeaderParityAgainstFixture(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)

	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load encoder variants fixture: %v", err)
	}

	expectedCoverage := 28

	covered := 0
	for _, c := range fixture.Cases {
		if c.Mode != fixtureModeName(encoder.ModeCELT) {
			continue
		}
		testName := fmt.Sprintf("%s-%s", c.Name, c.Variant)
		c := c
		t.Run(testName, func(t *testing.T) {
			assertCELTVariantPostfilterHeaderParityForCase(t, c)
		})
		covered++
	}
	if covered != expectedCoverage {
		t.Fatalf("CELT header fixture coverage mismatch: got=%d want=%d", covered, expectedCoverage)
	}
}

func assertCELTVariantPostfilterHeaderParityForCase(t *testing.T, fixtureCase encoderComplianceVariantsFixtureCase) {
	t.Helper()

	totalSamples := fixtureCase.SignalFrames * fixtureCase.FrameSize * fixtureCase.Channels
	signal, err := testsignal.GenerateEncoderSignalVariant(
		fixtureCase.Variant,
		48000,
		totalSamples,
		fixtureCase.Channels,
	)
	if err != nil {
		t.Fatalf("generate signal: %v", err)
	}
	if got := testsignal.HashFloat32LE(signal); got != fixtureCase.SignalSHA256 {
		t.Fatalf("signal hash mismatch: got=%s want=%s", got, fixtureCase.SignalSHA256)
	}

	ref, err := runPairedLibopusVariantPacketReference(fixtureCase, signal)
	if err != nil {
		t.Fatalf("run matched live libopus reference: %v", err)
	}
	goPackets, goRanges, err := encodeGopusForVariantsCase(fixtureCase, signal)
	if err != nil {
		t.Fatalf("encode gopus packets: %v", err)
	}
	comparison := compareEncoderPacketRanges(ref.packets, ref.finalRanges, goPackets, goRanges)
	logEncoderVariantPacketReference(t, fixtureCase, ref, comparison)

	libDec := celt.NewDecoder(fixtureCase.Channels)
	goDec := celt.NewDecoder(fixtureCase.Channels)

	var mismatches []string
	frames := min(len(ref.packets), len(goPackets))
	for i := 0; i < frames; i++ {
		got, err := decodeCELTPostfilterHeader(goDec, goPackets[i], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("decode gopus header frame %d: %v", i, err)
		}
		want, err := decodeCELTPostfilterHeader(libDec, ref.packets[i], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("decode live libopus header frame %d: %v", i, err)
		}
		if got != want {
			mismatches = append(mismatches, fmt.Sprintf("frame %d: got pitch=%d qg=%d tap=%d, want pitch=%d qg=%d tap=%d",
				i, got.period, got.qg, got.tapset, want.period, want.qg, want.tapset))
		}
	}

	for _, msg := range mismatches {
		t.Log(msg)
	}
	if len(mismatches) > 0 {
		t.Fatalf("CELT postfilter header mismatches: %d/%d; packet/range evidence: %s", len(mismatches), frames, comparison.summary())
	}
	if !comparison.countsExact() {
		t.Fatalf("CELT packet/range count mismatch; postfilter header mismatches=%d/%d; %s", len(mismatches), frames, comparison.summary())
	}
}

func decodeCELTPostfilterHeader(dec *celt.Decoder, packet []byte, frameSize int) (celtPostfilterHeader, error) {
	if len(packet) < 2 {
		return celtPostfilterHeader{}, fmt.Errorf("packet too short: %d", len(packet))
	}
	if getModeFromConfig(packet[0]>>3) != "CELT" {
		return celtPostfilterHeader{}, fmt.Errorf("unexpected non-CELT config %d", packet[0]>>3)
	}

	var rd rangecoding.Decoder
	rd.Init(packet[1:])
	if _, err := dec.DecodeFrameWithDecoder(&rd, frameSize); err != nil {
		return celtPostfilterHeader{}, err
	}
	return celtPostfilterHeader{
		period: dec.PostfilterPeriod(),
		qg:     qgFromPostfilterGain(dec.PostfilterGain()),
		tapset: dec.PostfilterTapset(),
	}, nil
}

func qgFromPostfilterGain(gain float32) int {
	if gain <= 0 {
		return 0
	}
	return int(math.Round(float64(gain)/0.09375)) - 1
}
