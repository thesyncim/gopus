package testvectors

import (
	"encoding/base64"
	"fmt"
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

func frameSNRAtIndex(ref, got []float32, frameSamples, frameIndex int) (float64, error) {
	start := frameIndex * frameSamples
	end := start + frameSamples
	if end > len(ref) || end > len(got) {
		return 0, fmt.Errorf("frame %d out of range (ref=%d got=%d)", frameIndex, len(ref), len(got))
	}
	return computeTestSNR(ref[start:end], got[start:end]), nil
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
		name     string
		minSNRDB float64
	}{
		{name: "hybrid-fb-10ms-mono-24k", minSNRDB: 5.0},
		{name: "hybrid-fb-10ms-stereo-24k", minSNRDB: 4.0},
		{name: "hybrid-swb-10ms-mono-24k", minSNRDB: 4.0},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
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
			snrDB, err := frameSNRAtIndex(refDecoded, gotDecoded, frameSamples, transitionIdx)
			if err != nil {
				t.Fatalf("transition frame snr: %v", err)
			}
			t.Logf("transition frame=%d snr=%.2f dB", transitionIdx, snrDB)
			if snrDB < tc.minSNRDB {
				t.Fatalf("transition parity regressed: SNR=%.2f dB < %.2f dB", snrDB, tc.minSNRDB)
			}

			// The following CELT frame should remain in near-bit-exact territory.
			if transitionIdx+1 < c.Frames {
				nextSNR, err := frameSNRAtIndex(refDecoded, gotDecoded, frameSamples, transitionIdx+1)
				if err != nil {
					t.Fatalf("next frame snr: %v", err)
				}
				t.Logf("next frame=%d snr=%.2f dB", transitionIdx+1, nextSNR)
				if nextSNR < 80.0 {
					t.Fatalf("post-transition celt parity regressed: SNR=%.2f dB < 80 dB", nextSNR)
				}
			}
		})
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
