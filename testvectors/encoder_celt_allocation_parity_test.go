//go:build gopus_libopus_oracle

package testvectors

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/testsignal"
)

func TestEncoderVariantCELTAllocationParityAgainstFixture(t *testing.T) {
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
			assertCELTVariantBandAllocationParityForCase(t, c)
		})
		covered++
	}
	if covered != expectedCoverage {
		t.Fatalf("CELT allocation fixture coverage mismatch: got=%d want=%d", covered, expectedCoverage)
	}
}

func assertCELTVariantBandAllocationParityForCase(t *testing.T, fixtureCase encoderComplianceVariantsFixtureCase) {
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
	identicalPackets := 0
	frames := min(len(ref.packets), len(goPackets))
	for i := 0; i < frames; i++ {
		if bytes.Equal(goPackets[i], ref.packets[i]) {
			identicalPackets++
		}
		got, err := probeCELTBandAllocation(goDec, goPackets[i], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("probe gopus allocation frame %d: %v", i, err)
		}
		want, err := probeCELTBandAllocation(libDec, ref.packets[i], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("probe live libopus allocation frame %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, want) {
			mismatches = append(mismatches, fmt.Sprintf("frame %d: got %+v want %+v", i, got, want))
		}
	}

	t.Logf("allocation mismatches=%d/%d byte_identical_packets=%d/%d; packet/range: %s",
		len(mismatches), frames, identicalPackets, frames, comparison.summary())

	for _, msg := range mismatches {
		t.Log(msg)
	}
	if len(mismatches) > 0 {
		t.Fatalf("CELT band allocation mismatches: %d/%d; packet/range: %s", len(mismatches), frames, comparison.summary())
	}
	if !comparison.countsExact() {
		t.Fatalf("CELT packet/range count mismatch; allocation mismatches=%d/%d; %s", len(mismatches), frames, comparison.summary())
	}
}

func probeCELTBandAllocation(dec *celt.Decoder, packet []byte, frameSize int) (celt.BandAllocationProbe, error) {
	if len(packet) < 2 {
		return celt.BandAllocationProbe{}, fmt.Errorf("packet too short: %d", len(packet))
	}
	if getModeFromConfig(packet[0]>>3) != "CELT" {
		return celt.BandAllocationProbe{}, fmt.Errorf("unexpected non-CELT config %d", packet[0]>>3)
	}

	var rd rangecoding.Decoder
	rd.Init(packet[1:])
	return dec.ProbeBandAllocationWithDecoder(&rd, frameSize)
}
