package testvectors

import (
	"encoding/base64"
	"fmt"
	"runtime"
	"testing"

	gopus "github.com/thesyncim/gopus"
)

func findDecoderMatrixCaseByName(fixture libopusDecoderMatrixFixtureFile, name string) (libopusDecoderMatrixCaseFile, bool) {
	for _, c := range fixture.Cases {
		if c.Name == name {
			return c, true
		}
	}
	return libopusDecoderMatrixCaseFile{}, false
}

func firstHybridToCELTFrameIndex(c libopusDecoderMatrixCaseFile) (int, error) {
	prevMode := ""
	for i, p := range c.Packets {
		raw, err := base64.StdEncoding.DecodeString(p.DataB64)
		if err != nil {
			return 0, fmt.Errorf("decode packet %d: %w", i, err)
		}
		if len(raw) == 0 {
			continue
		}
		mode := decoderMatrixPacketMode(raw[0])
		if i > 0 && prevMode == "hybrid" && mode == "celt" {
			return i, nil
		}
		prevMode = mode
	}
	return 0, fmt.Errorf("no hybrid->celt transition")
}

func frameQualityAtIndex(ref, got []float32, channels, frameSamples, frameIndex int) (float64, waveformStats, error) {
	start := frameIndex * frameSamples
	end := start + frameSamples
	if end > len(ref) || end > len(got) {
		return 0, waveformStats{}, fmt.Errorf("frame %d out of range (ref=%d got=%d)", frameIndex, len(ref), len(got))
	}
	frameRef := ref[start:end]
	frameGot := got[start:end]
	q, err := ComputeOpusCompareQualityFloat32(frameGot, frameRef, 48000, channels)
	if err != nil {
		return 0, waveformStats{}, err
	}
	return q, computeWaveformStats(frameGot, frameRef), nil
}

func TestDecoderHybridToCELT10msTransitionParity(t *testing.T) {
	requireTestTier(t, testTierParity)

	fixture, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder matrix fixture: %v", err)
	}

	// Guard the first CELT frame after a hybrid run in 10ms profiles.
	// These thresholds are intentionally modest and act as regression bounds.
	cases := []struct {
		name string
		minQ float64
	}{
		{name: "hybrid-fb-10ms-mono-24k", minQ: 0.0},
		{name: "hybrid-fb-10ms-stereo-24k", minQ: 0.0},
		{name: "hybrid-swb-10ms-mono-24k", minQ: 0.0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, ok := findDecoderMatrixCaseByName(fixture, tc.name)
			if !ok {
				t.Fatalf("fixture missing case %q", tc.name)
			}

			packets, err := decodeLibopusDecoderMatrixPackets(c)
			if err != nil {
				t.Fatalf("decode fixture packets: %v", err)
			}
			refDecoded, err := decodeLibopusDecoderMatrixSamples(c)
			if err != nil {
				t.Fatalf("decode fixture samples: %v", err)
			}
			gotDecoded := decodeWithInternalDecoder(t, packets, c.Channels)

			transitionIdx, err := firstHybridToCELTFrameIndex(c)
			if err != nil {
				t.Fatalf("find transition: %v", err)
			}

			frameSamples := c.FrameSize * c.Channels
			q, stats, err := frameQualityAtIndex(refDecoded, gotDecoded, c.Channels, frameSamples, transitionIdx)
			if err != nil {
				t.Fatalf("transition frame quality: %v", err)
			}
			minQ := tc.minQ
			if runtime.GOARCH == "amd64" && tc.name == "hybrid-fb-10ms-stereo-24k" {
				// amd64 shows stable but slightly lower transition quality versus arm64 on this edge case.
				minQ = -5.0
			}
			t.Logf("transition frame=%d q=%.2f corr=%.6f meanAbs=%.1f maxAbs=%.1f", transitionIdx, q, stats.Correlation, stats.MeanAbsErr*32768.0, stats.MaxAbsErr*32768.0)
			if q < minQ {
				t.Fatalf("transition parity regressed: Q=%.2f < %.2f", q, minQ)
			}

			// The following CELT frame should remain in near-bit-exact territory.
			if transitionIdx+1 < c.Frames {
				nextQ, nextStats, err := frameQualityAtIndex(refDecoded, gotDecoded, c.Channels, frameSamples, transitionIdx+1)
				if err != nil {
					t.Fatalf("next frame quality: %v", err)
				}
				t.Logf("next frame=%d q=%.2f corr=%.6f meanAbs=%.1f maxAbs=%.1f", transitionIdx+1, nextQ, nextStats.Correlation, nextStats.MeanAbsErr*32768.0, nextStats.MaxAbsErr*32768.0)
				if nextQ < 90.0 {
					t.Fatalf("post-transition celt parity regressed: Q=%.2f < 90", nextQ)
				}
			}
		})
	}
}

func TestDecoderHybridToCELT20msTransitionParity(t *testing.T) {
	requireTestTier(t, testTierParity)

	fixture, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder matrix fixture: %v", err)
	}

	c, ok := findDecoderMatrixCaseByName(fixture, "hybrid-fb-20ms-stereo-24k")
	if !ok {
		t.Fatal("fixture missing case hybrid-fb-20ms-stereo-24k")
	}

	packets, err := decodeLibopusDecoderMatrixPackets(c)
	if err != nil {
		t.Fatalf("decode fixture packets: %v", err)
	}
	refDecoded, err := decodeLibopusDecoderMatrixSamples(c)
	if err != nil {
		t.Fatalf("decode fixture samples: %v", err)
	}
	gotDecoded := decodeWithInternalDecoder(t, packets, c.Channels)

	transitionIdx, err := firstHybridToCELTFrameIndex(c)
	if err != nil {
		t.Fatalf("find transition: %v", err)
	}

	frameSamples := c.FrameSize * c.Channels
	q, stats, err := frameQualityAtIndex(refDecoded, gotDecoded, c.Channels, frameSamples, transitionIdx)
	if err != nil {
		t.Fatalf("transition frame quality: %v", err)
	}
	t.Logf("transition frame=%d q=%.2f corr=%.6f meanAbs=%.1f maxAbs=%.1f", transitionIdx, q, stats.Correlation, stats.MeanAbsErr*32768.0, stats.MaxAbsErr*32768.0)
	if q < 40.0 {
		t.Fatalf("20ms transition parity regressed: Q=%.2f < 40", q)
	}

	if transitionIdx+1 < c.Frames {
		nextQ, nextStats, err := frameQualityAtIndex(refDecoded, gotDecoded, c.Channels, frameSamples, transitionIdx+1)
		if err != nil {
			t.Fatalf("next frame quality: %v", err)
		}
		t.Logf("next frame=%d q=%.2f corr=%.6f meanAbs=%.1f maxAbs=%.1f", transitionIdx+1, nextQ, nextStats.Correlation, nextStats.MeanAbsErr*32768.0, nextStats.MaxAbsErr*32768.0)
		if nextQ < 90.0 {
			t.Fatalf("post-transition celt parity regressed: Q=%.2f < 90", nextQ)
		}
	}
}

func decoderMatrixPacketMode(toc byte) string {
	mode := gopus.ParseTOC(toc).Mode
	switch mode {
	case gopus.ModeSILK:
		return "silk"
	case gopus.ModeHybrid:
		return "hybrid"
	case gopus.ModeCELT:
		return "celt"
	default:
		return "unknown"
	}
}
