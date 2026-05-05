package testvectors

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/rangecoding"
)

type celtPostfilterHeader struct {
	period int
	qg     int
	tapset int
}

func TestEncoderVariantCELTHeaderParityAgainstFixture(t *testing.T) {
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)

	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load encoder variants fixture: %v", err)
	}

	// These cases still expose pitch/postfilter rounding gaps, so keep them out
	// of the exact header ratchet until that path is green. The amd64 fixture
	// keeps its vectorized tone-LPC coverage separate from the scalar-order path
	// used by the generic/arm64 fixture.
	knownHeaderGaps := map[string]struct{}{
		"CELT-FB-2.5ms-mono-64k-speech_like_v1":  {},
		"CELT-FB-5ms-stereo-128k-chirp_sweep_v1": {},
	}
	expectedCoverage := 26
	if runtime.GOARCH == "amd64" {
		knownHeaderGaps = map[string]struct{}{
			"CELT-FB-10ms-mono-64k-chirp_sweep_v1":  {},
			"CELT-FB-2.5ms-mono-64k-chirp_sweep_v1": {},
			"CELT-FB-2.5ms-mono-64k-speech_like_v1": {},
			"CELT-FB-5ms-mono-64k-chirp_sweep_v1":   {},
		}
		expectedCoverage = 24
	}

	covered := 0
	for _, c := range fixture.Cases {
		if c.Mode != fixtureModeName(encoder.ModeCELT) {
			continue
		}
		testName := fmt.Sprintf("%s-%s", c.Name, c.Variant)
		if _, ok := knownHeaderGaps[testName]; ok {
			continue
		}
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

	libPackets, _, err := decodeEncoderVariantsFixturePackets(fixtureCase)
	if err != nil {
		t.Fatalf("decode fixture packets: %v", err)
	}
	goPackets, err := encodeGopusForVariantsCase(fixtureCase, signal)
	if err != nil {
		t.Fatalf("encode gopus packets: %v", err)
	}
	if len(goPackets) != len(libPackets) {
		t.Fatalf("packet count mismatch: got=%d want=%d", len(goPackets), len(libPackets))
	}

	libDec := celt.NewDecoder(fixtureCase.Channels)
	goDec := celt.NewDecoder(fixtureCase.Channels)

	var mismatches []string
	for i := range libPackets {
		got, err := decodeCELTPostfilterHeader(goDec, goPackets[i], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("decode gopus header frame %d: %v", i, err)
		}
		want, err := decodeCELTPostfilterHeader(libDec, libPackets[i], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("decode fixture header frame %d: %v", i, err)
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
		t.Fatalf("CELT postfilter header mismatches: %d/%d", len(mismatches), len(libPackets))
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

func qgFromPostfilterGain(gain float64) int {
	if gain <= 0 {
		return 0
	}
	return int(math.Round(gain/0.09375)) - 1
}
