package testvectors

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/testsignal"
)

func encoderVariantReferenceSettings(c encoderComplianceVariantsFixtureCase) (encoderQualityReferenceSettings, error) {
	mode, err := parseFixtureMode(c.Mode)
	if err != nil {
		return encoderQualityReferenceSettings{}, fmt.Errorf("parse mode %q: %w", c.Mode, err)
	}
	bandwidth, err := parseFixtureBandwidth(c.Bandwidth)
	if err != nil {
		return encoderQualityReferenceSettings{}, fmt.Errorf("parse bandwidth %q: %w", c.Bandwidth, err)
	}
	if c.FrameSize <= 0 || c.Channels <= 0 || c.Bitrate <= 0 || c.SignalFrames <= 0 {
		return encoderQualityReferenceSettings{}, fmt.Errorf("invalid case settings: frame=%d channels=%d bitrate=%d signal_frames=%d", c.FrameSize, c.Channels, c.Bitrate, c.SignalFrames)
	}
	if c.Frames != c.SignalFrames+1 {
		return encoderQualityReferenceSettings{}, fmt.Errorf("case frames=%d, want %d signal frames plus one flush frame", c.Frames, c.SignalFrames)
	}
	return encoderQualityReferenceSettings{
		mode:      mode,
		bandwidth: bandwidth,
		frameSize: c.FrameSize,
		channels:  c.Channels,
		bitrate:   c.Bitrate,
	}, nil
}

func runPairedLibopusVariantPacketReference(c encoderComplianceVariantsFixtureCase, signal []float32) (encoderPacketReference, error) {
	wantSamples := c.SignalFrames * c.FrameSize * c.Channels
	if len(signal) != wantSamples {
		return encoderPacketReference{}, fmt.Errorf("signal sample count=%d, want %d", len(signal), wantSamples)
	}
	pcmSHA256 := testsignal.HashFloat32LE(signal)
	if pcmSHA256 != c.SignalSHA256 {
		return encoderPacketReference{}, fmt.Errorf("signal hash mismatch: got=%s want=%s", pcmSHA256, c.SignalSHA256)
	}
	settings, err := encoderVariantReferenceSettings(c)
	if err != nil {
		return encoderPacketReference{}, err
	}
	ref, err := runPairedLibopusPacketReference(settings, signal)
	if err != nil {
		return encoderPacketReference{}, err
	}
	if ref.pcmSHA256 != c.SignalSHA256 {
		return encoderPacketReference{}, fmt.Errorf("paired reference PCM hash mismatch: got=%s want=%s", ref.pcmSHA256, c.SignalSHA256)
	}
	if len(ref.packets) != c.Frames || len(ref.finalRanges) != c.Frames {
		return encoderPacketReference{}, fmt.Errorf("paired reference produced packets=%d ranges=%d, want %d signal+flush frames", len(ref.packets), len(ref.finalRanges), c.Frames)
	}
	return ref, nil
}

type encoderPacketRangeComparison struct {
	goPacketCount, referencePacketCount int
	goRangeCount, referenceRangeCount   int
	packetFramesCompared                int
	packetFramesDiffer                  int
	firstPacketFrame                    int
	firstPacketByte                     int
	firstPacketGoLength                 int
	firstPacketReferenceLength          int
	firstPacketGoPrefix                 string
	firstPacketReferencePrefix          string
	firstPacketGoRange                  uint32
	firstPacketReferenceRange           uint32
	firstPacketHasRanges                bool
	rangeFramesCompared                 int
	rangeFramesDiffer                   int
	firstRangeFrame                     int
	firstRangeGo                        uint32
	firstRangeReference                 uint32
}

func compareEncoderPacketRanges(referencePackets [][]byte, referenceRanges []uint32, goPackets [][]byte, goRanges []uint32) encoderPacketRangeComparison {
	comparison := encoderPacketRangeComparison{
		goPacketCount:        len(goPackets),
		referencePacketCount: len(referencePackets),
		goRangeCount:         len(goRanges),
		referenceRangeCount:  len(referenceRanges),
		firstPacketFrame:     -1,
		firstPacketByte:      -1,
		firstRangeFrame:      -1,
	}
	comparison.packetFramesCompared = min(len(referencePackets), len(goPackets))
	for frame := 0; frame < comparison.packetFramesCompared; frame++ {
		if bytes.Equal(referencePackets[frame], goPackets[frame]) {
			continue
		}
		comparison.packetFramesDiffer++
		if comparison.firstPacketFrame >= 0 {
			continue
		}
		comparison.firstPacketFrame = frame
		comparison.firstPacketGoLength = len(goPackets[frame])
		comparison.firstPacketReferenceLength = len(referencePackets[frame])
		comparison.firstPacketByte = firstPacketDifferenceOffset(referencePackets[frame], goPackets[frame])
		comparison.firstPacketGoPrefix = boundedPacketHexPrefix(goPackets[frame])
		comparison.firstPacketReferencePrefix = boundedPacketHexPrefix(referencePackets[frame])
		if frame < len(goRanges) && frame < len(referenceRanges) {
			comparison.firstPacketHasRanges = true
			comparison.firstPacketGoRange = goRanges[frame]
			comparison.firstPacketReferenceRange = referenceRanges[frame]
		}
	}

	comparison.rangeFramesCompared = min(len(referenceRanges), len(goRanges))
	for frame := 0; frame < comparison.rangeFramesCompared; frame++ {
		if referenceRanges[frame] == goRanges[frame] {
			continue
		}
		comparison.rangeFramesDiffer++
		if comparison.firstRangeFrame < 0 {
			comparison.firstRangeFrame = frame
			comparison.firstRangeGo = goRanges[frame]
			comparison.firstRangeReference = referenceRanges[frame]
		}
	}
	return comparison
}

func (c encoderPacketRangeComparison) countsExact() bool {
	return c.goPacketCount == c.goRangeCount &&
		c.referencePacketCount == c.referenceRangeCount &&
		c.goPacketCount == c.referencePacketCount &&
		c.goRangeCount == c.referenceRangeCount
}

func (c encoderPacketRangeComparison) exact() bool {
	return c.countsExact() &&
		c.packetFramesDiffer == 0 && c.rangeFramesDiffer == 0
}

func (c encoderPacketRangeComparison) summary() string {
	var parts []string
	parts = append(parts, fmt.Sprintf("packet_count Go=%d C=%d diff_frames=%d/%d", c.goPacketCount, c.referencePacketCount, c.packetFramesDiffer, c.packetFramesCompared))
	parts = append(parts, fmt.Sprintf("range_count Go=%d C=%d diff_frames=%d/%d", c.goRangeCount, c.referenceRangeCount, c.rangeFramesDiffer, c.rangeFramesCompared))
	if c.firstPacketFrame >= 0 {
		first := fmt.Sprintf("first_packet frame=%d byte=%d len(Go=%d C=%d) prefix(Go=%s C=%s)",
			c.firstPacketFrame, c.firstPacketByte, c.firstPacketGoLength, c.firstPacketReferenceLength,
			c.firstPacketGoPrefix, c.firstPacketReferencePrefix)
		if c.firstPacketHasRanges {
			first += fmt.Sprintf(" range(Go=0x%08x C=0x%08x)", c.firstPacketGoRange, c.firstPacketReferenceRange)
		}
		parts = append(parts, first)
	}
	if c.firstRangeFrame >= 0 {
		parts = append(parts, fmt.Sprintf("first_range frame=%d Go=0x%08x C=0x%08x", c.firstRangeFrame, c.firstRangeGo, c.firstRangeReference))
	}
	return strings.Join(parts, "; ")
}

func firstPacketDifferenceOffset(reference, got []byte) int {
	limit := min(len(reference), len(got))
	for i := 0; i < limit; i++ {
		if reference[i] != got[i] {
			return i
		}
	}
	return limit
}

func boundedPacketHexPrefix(packet []byte) string {
	if len(packet) == 0 {
		return "<empty>"
	}
	return hex.EncodeToString(packet[:min(len(packet), 12)])
}

func logEncoderVariantPacketReference(t *testing.T, c encoderComplianceVariantsFixtureCase, ref encoderPacketReference, comparison encoderPacketRangeComparison) {
	t.Helper()
	t.Logf("live paired reference: case=%s variant=%s %s pcm_sha256=%s; %s", c.Name, c.Variant, ref.identity, ref.pcmSHA256, comparison.summary())
}
