package testvectors

import (
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
)

type variantProvenanceAuditRow struct {
	name         string
	mode         string
	variant      string
	gapDB        float64
	modeMismatch float64
	histogramL1  float64
}

func encodeGopusForVariantsCaseWithProvenance(c encoderComplianceVariantsFixtureCase, signal []float32) ([][]byte, error) {
	mode, err := parseFixtureMode(c.Mode)
	if err != nil {
		return nil, err
	}
	bandwidth, err := parseFixtureBandwidth(c.Bandwidth)
	if err != nil {
		return nil, err
	}

	enc := encoder.NewEncoder(48000, c.Channels)
	// Fixture rows tagged as "hybrid" are generated with opus_demo "audio"
	// application, which allows adaptive SILK/CELT mode selection.
	encMode := mode
	if mode == encoder.ModeHybrid {
		encMode = encoder.ModeAuto
	}
	enc.SetMode(encMode)
	enc.SetBandwidth(bandwidth)
	enc.SetBitrate(c.Bitrate)
	enc.SetBitrateMode(encoder.ModeCBR)
	// Match fixture generation provenance: do not force signal-type hints.

	packets := make([][]byte, c.SignalFrames)
	samplesPerFrame := c.FrameSize * c.Channels
	for i := 0; i < c.SignalFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		frame := float32ToFloat64OpusDemoF32(signal[start:end])
		pkt, err := enc.Encode(frame, c.FrameSize)
		if err != nil {
			return nil, fmt.Errorf("encode frame %d: %w", i, err)
		}
		if len(pkt) == 0 {
			return nil, fmt.Errorf("empty packet at frame %d", i)
		}
		pktCopy := make([]byte, len(pkt))
		copy(pktCopy, pkt)
		packets[i] = pktCopy
	}
	return packets, nil
}

func provenanceGapFloor(mode string) float64 {
	switch mode {
	case "celt":
		return -20.0
	case "silk":
		return -25.0
	case "hybrid":
		return -45.0
	default:
		return -45.0
	}
}

func TestEncoderVariantProfileProvenanceAudit(t *testing.T) {
	requireTestTier(t, testTierExhaustive)

	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load encoder variants fixture: %v", err)
	}

	rows := make([]variantProvenanceAuditRow, 0, len(fixture.Cases))
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
			goQ, err := qualityFromPacketsInternal(goPackets, signal, c.Channels, c.FrameSize)
			if err != nil {
				t.Fatalf("compute gopus quality: %v", err)
			}
			libQ, err := qualityFromPacketsInternal(libPackets, signal, c.Channels, c.FrameSize)
			if err != nil {
				t.Fatalf("compute libopus quality from fixture: %v", err)
			}
			gapDB := SNRFromQuality(goQ) - SNRFromQuality(libQ)
			if math.IsNaN(gapDB) || math.IsInf(gapDB, 0) {
				t.Fatalf("invalid quality gap: %v", gapDB)
			}
			if gapDB < provenanceGapFloor(c.Mode) {
				t.Fatalf("catastrophic provenance gap: %.2f dB (mode=%s)", gapDB, c.Mode)
			}

			rows = append(rows, variantProvenanceAuditRow{
				name:         c.Name,
				mode:         c.Mode,
				variant:      c.Variant,
				gapDB:        gapDB,
				modeMismatch: stats.modeMismatchRate,
				histogramL1:  stats.histogramL1,
			})
		})
	}

	if len(rows) == 0 {
		t.Fatal("provenance audit produced no rows")
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].gapDB < rows[j].gapDB
	})

	const topN = 8
	n := topN
	if len(rows) < n {
		n = len(rows)
	}
	for i := 0; i < n; i++ {
		r := rows[i]
		t.Logf("worst[%d]: %s[%s] mode=%s gap=%.2fdB mismatch=%.2f%% histL1=%.3f",
			i+1, r.name, r.variant, r.mode, r.gapDB, 100*r.modeMismatch, r.histogramL1)
	}
}
