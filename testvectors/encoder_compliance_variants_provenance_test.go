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

func encodeGopusForVariantsCaseWithProvenance(c encoderComplianceVariantsFixtureCase, signal []float32) ([][]byte, error) {
	return encodeGopusForVariantsCase(c, signal)
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
	requireTestTier(t, testTierExhaustive)

	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load encoder variants fixture: %v", err)
	}

	rows := make([]variantProvenanceAuditRow, 0, len(fixture.Cases))
	severeCount := 0
	for _, c := range fixture.Cases {
		c := c
		name := fmt.Sprintf("%s-%s", c.Name, c.Variant)
		t.Run(name, func(t *testing.T) {
			totalSamples := c.SignalFrames * c.FrameSize * c.Channels
			signal, err := testsignal.GenerateEncoderSignalVariant(c.Variant, 48000, totalSamples, c.Channels)
			if err != nil {
				t.Fatalf("generate signal: %v", err)
			}
			if hash := testsignal.HashFloat32LE(signal); hash != c.SignalSHA256 {
				t.Fatalf("signal hash mismatch for %s", name)
			}

			libPackets, _, err := decodeEncoderVariantsFixturePackets(c)
			if err != nil {
				t.Fatalf("decode fixture packets: %v", err)
			}
			goPackets, err := encodeGopusForVariantsCaseWithProvenance(c, signal)
			if err != nil {
				t.Fatalf("encode gopus packets with fixture provenance: %v", err)
			}

			packetCountDiff := len(goPackets) - len(libPackets)
			if packetCountDiff < 0 {
				packetCountDiff = -packetCountDiff
			}
			if packetCountDiff > 1 {
				t.Fatalf("packet count mismatch: go=%d lib=%d", len(goPackets), len(libPackets))
			}

			stats := computeEncoderPacketProfileStats(libPackets, goPackets)
			goQ, err := qualityFromPacketsLibopusReference(goPackets, signal, c.Channels, c.FrameSize)
			if err != nil {
				t.Fatalf("compute gopus quality with libopus decode: %v", err)
			}
			libQ, err := qualityFromPacketsLibopusReference(libPackets, signal, c.Channels, c.FrameSize)
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
		})
	}

	if len(rows) == 0 {
		t.Fatal("provenance audit produced no rows")
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].gapQ < rows[j].gapQ
	})

	const topN = 8
	n := topN
	if len(rows) < n {
		n = len(rows)
	}
	for i := 0; i < n; i++ {
		r := rows[i]
		severity := ""
		if r.severe {
			severity = " severe"
		}
		t.Logf("worst[%d]: %s[%s] mode=%s gap=%.2fQ mismatch=%.2f%% histL1=%.3f%s",
			i+1, r.name, r.variant, r.mode, r.gapQ, 100*r.modeMismatch, r.histogramL1, severity)
	}
	t.Logf("severe provenance gaps: %d/%d", severeCount, len(rows))
}
