//go:build gopus_libopus_oracle

package testvectors

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/rangecoding"
)

// allocationMismatchRateCeiling is the maximum allowed fraction of frames whose
// decoded band allocation differs between libopus and gopus CELT packets.
// Postfilter headers are checked bit-exact in TestEncoderVariantCELTHeaderParityAgainstFixture;
// allocation is more sensitive to non-byte-identical encoder outputs.
const allocationMismatchRateCeiling = 0.22

func TestEncoderVariantCELTAllocationParityAgainstFixture(t *testing.T) {
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
	identicalPackets := 0
	for i := range libPackets {
		if bytes.Equal(goPackets[i], libPackets[i]) {
			identicalPackets++
		}
		got, err := probeCELTBandAllocation(goDec, goPackets[i], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("probe gopus allocation frame %d: %v", i, err)
		}
		want, err := probeCELTBandAllocation(libDec, libPackets[i], fixtureCase.FrameSize)
		if err != nil {
			t.Fatalf("probe fixture allocation frame %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, want) {
			mismatches = append(mismatches, fmt.Sprintf("frame %d: got %+v want %+v", i, got, want))
		}
	}

	mismatchRate := float64(len(mismatches)) / float64(len(libPackets))
	t.Logf("allocation mismatches=%d/%d (%.2f%%) byte_identical_packets=%d/%d",
		len(mismatches), len(libPackets), 100*mismatchRate, identicalPackets, len(libPackets))

	for _, msg := range mismatches {
		t.Log(msg)
	}
	if mismatchRate > allocationMismatchRateCeiling {
		t.Fatalf("CELT band allocation mismatch rate regression: %.2f%% > %.2f%% ceiling",
			100*mismatchRate, 100*allocationMismatchRateCeiling)
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
