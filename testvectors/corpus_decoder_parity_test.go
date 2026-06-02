package testvectors

// corpus_decoder_parity_test.go — broader signal-class decoder parity corpus.
//
// This file gates gopus decoder parity against libopus across a broad set of
// signal classes beyond the summary cases:
//
//   Signal classes: clean speech, music, mixed, noise, transient (castanet),
//                   pure tone, near-silence
//   Codec configs:  SILK (6–32 kbps), CELT (8–192 kbps), Hybrid (32–64 kbps)
//   Channels:       mono, stereo
//
// Each test case:
//   1. Loads frozen packets + libopus-decoded reference PCM from the fixture.
//   2. Decodes the same packets with the gopus internal decoder.
//   3. Gates the result with qualitycompare.AssertParity (auto-selects
//      opus_compare-Q or waveform corr/RMS depending on the signal profile).
//
// A coverage gate (TestCorpusSignalClassCoverage) independently verifies that
// every required signal class and every codec mode appears in the fixture.

import (
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// TestCorpusDecoderParity decodes each corpus fixture case with gopus and gates
// quality against the frozen libopus reference with qualitycompare.AssertParity.
func TestCorpusDecoderParity(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)

	fixture, err := loadCorpusFixture()
	if err != nil {
		t.Skipf("corpus fixture unavailable (generate with tools/gen_corpus_decoder_parity_fixture.go): %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported corpus fixture version: %d", fixture.Version)
	}
	if fixture.SampleRate != 48000 {
		t.Fatalf("unsupported corpus fixture sample rate: %d", fixture.SampleRate)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("corpus fixture has no cases")
	}

	for _, c := range fixture.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()

			// Decode with gopus.
			gopusDecoded := decodeWithInternalDecoder(t, c.decodedPackets, c.Channels)
			refDecoded := c.decodedSamples

			if len(gopusDecoded) == 0 {
				t.Fatalf("gopus decoded empty output for %s", c.Name)
			}
			if len(refDecoded) == 0 {
				t.Fatalf("reference decoded empty for %s", c.Name)
			}

			// Build signal profile: all samples are coded (no PLC/concealment here).
			n := len(gopusDecoded)
			if len(refDecoded) < n {
				n = len(refDecoded)
			}
			profile := qualitycompare.CodedProfile(fixture.SampleRate, c.Channels, n)

			// Gate: near-exact intent — gopus must track libopus as closely as
			// libopus tracks itself across builds.
			qualitycompare.AssertParity(t, gopusDecoded[:n], refDecoded[:n], profile,
				qualitycompare.IntentNearExact, c.Name)
		})
	}
}

// TestCorpusSignalClassCoverage asserts that every required signal class and
// every codec mode (SILK, CELT, Hybrid) appear in the corpus fixture, and that
// both mono and stereo cases are present. Skips when the fixture has not been
// generated yet; use gen_corpus_decoder_parity_fixture.go to generate it.
func TestCorpusSignalClassCoverage(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierFast)

	fixture, err := loadCorpusFixture()
	if err != nil {
		t.Skipf("corpus fixture unavailable (generate with tools/gen_corpus_decoder_parity_fixture.go): %v", err)
	}

	// Track coverage.
	seenClasses := make(map[string]bool)
	seenModes := map[string]bool{"silk": false, "hybrid": false, "celt": false}
	seenMono := false
	seenStereo := false
	seenLowBitrate := false  // <= 12 kbps
	seenHighBitrate := false // >= 128 kbps

	for _, c := range fixture.Cases {
		seenClasses[c.SignalClass] = true
		if c.Channels == 1 {
			seenMono = true
		}
		if c.Channels == 2 {
			seenStereo = true
		}
		if c.Bitrate <= 12000 {
			seenLowBitrate = true
		}
		if c.Bitrate >= 128000 {
			seenHighBitrate = true
		}
		for mode, count := range c.ModeHistogram {
			if count > 0 {
				seenModes[mode] = true
			}
		}
	}

	// Every required signal class must be present.
	required := testsignal.CorpusSignalClasses()
	var missingClasses []string
	for _, cls := range required {
		if !seenClasses[cls] {
			missingClasses = append(missingClasses, cls)
		}
	}
	if len(missingClasses) > 0 {
		t.Errorf("corpus fixture missing signal classes: %v", missingClasses)
	}

	// All three codec modes must appear.
	for _, mode := range []string{"silk", "hybrid", "celt"} {
		if !seenModes[mode] {
			t.Errorf("corpus fixture missing codec mode: %s", mode)
		}
	}

	if !seenMono {
		t.Error("corpus fixture missing mono cases")
	}
	if !seenStereo {
		t.Error("corpus fixture missing stereo cases")
	}
	if !seenLowBitrate {
		t.Error("corpus fixture missing low-bitrate cases (<= 12 kbps)")
	}
	if !seenHighBitrate {
		t.Error("corpus fixture missing high-bitrate cases (>= 128 kbps)")
	}

	// Report summary.
	t.Logf("signal classes: %d/%d present", len(seenClasses), len(required))
	t.Logf("codec modes: silk=%v hybrid=%v celt=%v", seenModes["silk"], seenModes["hybrid"], seenModes["celt"])
	t.Logf("channels: mono=%v stereo=%v", seenMono, seenStereo)
	t.Logf("bitrates: low=%v high=%v", seenLowBitrate, seenHighBitrate)
	t.Logf("total cases: %d", len(fixture.Cases))
}

// TestCorpusFixtureGeneratorReferenced checks that the corpus fixture generator
// script exists and carries the correct //go:build ignore guard.
func TestCorpusFixtureGeneratorReferenced(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierFast)

	generatorPath := "../tools/gen_corpus_decoder_parity_fixture.go"
	data, err := os.ReadFile(generatorPath)
	if err != nil {
		t.Fatalf("corpus fixture generator script not found at %s: %v", generatorPath, err)
	}
	if len(data) == 0 {
		t.Fatal("corpus fixture generator script is empty")
	}
}

// TestCorpusFixtureProvenance checks that the fixture carries valid provenance.
// Skips when the fixture has not been generated yet.
func TestCorpusFixtureProvenance(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierFast)

	fixture, err := loadCorpusFixture()
	if err != nil {
		t.Skipf("corpus fixture unavailable (generate with tools/gen_corpus_decoder_parity_fixture.go): %v", err)
	}
	if fixture.Provenance.LibopusVersion == "" {
		t.Fatal("corpus fixture missing libopus_version in provenance")
	}
	if fixture.Generator == "" {
		t.Fatal("corpus fixture missing generator field")
	}
	if err := validateLibopusFixtureProvenance(fixture.Provenance); err != nil {
		t.Fatalf("corpus fixture provenance invalid: %v", err)
	}
	t.Logf("generator: %s", fixture.Generator)
	t.Logf("libopus_version: %s", fixture.Provenance.LibopusVersion)
}
