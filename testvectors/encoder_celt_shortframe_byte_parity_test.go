// CELT short-frame (2.5 ms / 5 ms) parity tests. Fixture metadata defines the
// input matrix and signal; the live paired libopus build supplies expected
// packets and final ranges.
package testvectors

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

// celtShortFrameCases is the 2.5ms/5ms portion of encoderComplianceSummaryCases
// that this file gates with byte-parity assertions.
type celtShortFrameCase struct {
	name      string
	frameSize int // samples at 48 kHz
	channels  int
	bitrate   int
}

func celtShortFrameByteParityCases() []celtShortFrameCase {
	return []celtShortFrameCase{
		{name: "CELT-FB-2.5ms-mono-64k", frameSize: 120, channels: 1, bitrate: 64000},
		{name: "CELT-FB-2.5ms-stereo-128k", frameSize: 120, channels: 2, bitrate: 128000},
		{name: "CELT-FB-5ms-mono-64k", frameSize: 240, channels: 1, bitrate: 64000},
		{name: "CELT-FB-5ms-stereo-128k", frameSize: 240, channels: 2, bitrate: 128000},
	}
}

func TestEncoderCELTShortFrameVariantByteParityAgainstLibopusFixture(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)

	variants := testsignal.EncoderSignalVariants()
	covered := 0
	for _, tc := range celtShortFrameByteParityCases() {
		for _, variant := range variants {
			tc, variant := tc, variant
			t.Run(fmt.Sprintf("%s-%s", tc.name, variant), func(t *testing.T) {
				t.Parallel()
				fixtureCase, ok := findEncoderVariantsFixtureCase(
					encoder.ModeCELT,
					types.BandwidthFullband,
					tc.frameSize,
					tc.channels,
					tc.bitrate,
					variant,
				)
				if !ok {
					t.Fatalf("variant fixture case not found for %s[%s]", tc.name, variant)
				}

				totalSamples := fixtureCase.SignalFrames * fixtureCase.FrameSize * fixtureCase.Channels
				signal, err := testsignal.GenerateEncoderSignalVariant(variant, 48000, totalSamples, tc.channels)
				if err != nil {
					t.Fatalf("generate signal: %v", err)
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
				if !comparison.exact() {
					t.Fatalf("CELT short-frame live libopus parity failed: %s", comparison.summary())
				}
			})
			covered++
		}
	}
	if covered != 16 {
		t.Fatalf("CELT short-frame case coverage mismatch: got=%d want=16", covered)
	}
}
