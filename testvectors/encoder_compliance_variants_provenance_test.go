package testvectors

import (
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/thesyncim/gopus/internal/testsignal"
)

type variantProvenanceAuditRow struct {
	name         string
	mode         string
	variant      string
	gapQ         float64
	severe       bool
	modeMismatch float64
	histogramL1  float64
}

func provenanceGapFloorQ(mode string) float64 {
	switch mode {
	case "celt":
		return -42.0
	case "silk":
		return -53.0
	case "hybrid":
		return -94.0
	default:
		return -94.0
	}
}

func TestEncoderVariantProfileProvenanceAudit(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierExhaustive)

	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load encoder variants fixture: %v", err)
	}

	rows := make([]variantProvenanceAuditRow, 0, len(fixture.Cases))
	severeCount := 0
	ranCases := 0
	for _, c := range fixture.Cases {
		name := fmt.Sprintf("%s-%s", c.Name, c.Variant)
		t.Run(name, func(t *testing.T) {
			ranCases++
			totalSamples := c.SignalFrames * c.FrameSize * c.Channels
			signal, err := testsignal.GenerateEncoderSignalVariant(c.Variant, 48000, totalSamples, c.Channels)
			if err != nil {
				t.Fatalf("generate signal: %v", err)
			}
			if hash := testsignal.HashFloat32LE(signal); hash != c.SignalSHA256 {
				t.Fatalf("signal hash mismatch for %s", name)
			}

			ref, err := runPairedLibopusVariantPacketReference(c, signal)
			if err != nil {
				t.Fatalf("run matched live libopus reference: %v", err)
			}
			goPackets, goRanges, err := encodeGopusForVariantsCase(c, signal)
			if err != nil {
				t.Fatalf("encode gopus packets: %v", err)
			}
			comparison := compareEncoderPacketRanges(ref.packets, ref.finalRanges, goPackets, goRanges)
			logEncoderVariantPacketReference(t, c, ref, comparison)

			stats := computeEncoderPacketProfileStats(ref.packets, goPackets)
			goQ, err := qualityFromPacketsLibopusReference(goPackets, signal, c.Channels, c.FrameSize)
			if err != nil {
				t.Fatalf("compute gopus quality with libopus decode: %v", err)
			}
			libQ, err := qualityFromPacketsLibopusReference(ref.packets, signal, c.Channels, c.FrameSize)
			if err != nil {
				t.Fatalf("compute libopus quality from fixture with libopus decode: %v", err)
			}
			gapQ := goQ - libQ
			if math.IsNaN(gapQ) || math.IsInf(gapQ, 0) {
				t.Fatalf("invalid quality gap: %v", gapQ)
			}
			severe := gapQ < provenanceGapFloorQ(c.Mode)
			if severe {
				severeCount++
				t.Logf("severe provenance gap: %.2f Q (mode=%s)", gapQ, c.Mode)
			}

			rows = append(rows, variantProvenanceAuditRow{
				name:         c.Name,
				mode:         c.Mode,
				variant:      c.Variant,
				gapQ:         gapQ,
				severe:       severe,
				modeMismatch: stats.modeMismatchRate,
				histogramL1:  stats.histogramL1,
			})
			if !comparison.exact() {
				t.Errorf("matched live packet/range parity failed: %s", comparison.summary())
			}
		})
	}

	if len(rows) != ranCases {
		t.Fatalf("provenance audit row coverage mismatch: got=%d ran=%d", len(rows), ranCases)
	}
	if ranCases == 0 {
		t.Fatal("provenance audit selected no cases")
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].gapQ < rows[j].gapQ
	})

	const topN = 8
	n := min(len(rows), topN)
	for i := 0; i < n; i++ {
		r := rows[i]
		severity := ""
		if r.severe {
			severity = " severe"
		}
		t.Logf("worst[%d]: %s[%s] mode=%s gap=%.2fQ mismatch=%.2f%% histL1=%.3f%s",
			i+1, r.name, r.variant, r.mode, r.gapQ, 100*r.modeMismatch, r.histogramL1, severity)
	}
	t.Logf("matched live provenance audit cases: %d/%d; severe quality gaps: %d", ranCases, len(fixture.Cases), severeCount)
}
