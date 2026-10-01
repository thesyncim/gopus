package testvectors

import (
	"encoding/base64"
	"fmt"
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
)

// transitionFrameComparison scores a single decoded frame (no delay search:
// both sides follow the same decode cadence so the frame index is aligned) via
// the canonical comparator. maxDelay=0 yields the delay-0-only opus_compare Q
// used for the per-frame quality diagnostic.
func transitionFrameComparison(ref, got []float32, channels, frameSamples, frameIndex int) (QualityComparison, error) {
	start := frameIndex * frameSamples
	end := start + frameSamples
	if end > len(ref) || end > len(got) {
		return QualityComparison{}, fmt.Errorf("frame %d out of range (ref=%d got=%d)", frameIndex, len(ref), len(got))
	}
	return CompareDecodedFloat32(got[start:end], ref[start:end], 48000, channels, 0)
}

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

// transitionPostBar guards the CELT frame immediately after a hybrid->CELT
// transition. The exact PCM assertion applies to the complete sequence; this
// Q-only check also requires a score of at least 90 for the following frame.
var transitionPostBar = QualityBar{MinQ: 90.0, Desc: "post-transition celt frame (Q>=90)"}

func TestDecoderHybridToCELT10msTransitionParity(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)

	fixture, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder matrix fixture: %v", err)
	}

	// Compare the entire selected-C sequence exactly and retain per-frame
	// quality diagnostics around the first Hybrid-to-CELT transition.
	cases := []struct {
		name string
		bar  QualityBar
	}{
		{name: "hybrid-fb-10ms-mono-24k", bar: QualityBar{MinQ: 0.0, Desc: "10ms hybrid->celt transition frame (Q>=0)"}},
		{name: "hybrid-fb-10ms-stereo-24k", bar: QualityBar{MinQ: 0.0, Desc: "10ms hybrid->celt transition frame (Q>=0)"}},
		{name: "hybrid-swb-10ms-mono-24k", bar: QualityBar{MinQ: 0.0, Desc: "10ms hybrid->celt transition frame (Q>=0)"}},
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
			refDecoded, err := decodeWithMatchedTierReferencePacketsSingle(fixture.SampleRate, c.Channels, c.FrameSize, packets)
			if err != nil {
				t.Fatalf("decode packets with matched libopus: %v", err)
			}
			gotDecoded := decodeWithInternalDecoder(t, packets, c.Channels)
			if len(gotDecoded) != len(refDecoded) {
				t.Fatalf("decoded length mismatch: Go=%d matched C=%d", len(gotDecoded), len(refDecoded))
			}

			assertTransitionSequenceExact(t, gotDecoded, refDecoded, c.FrameSize*c.Channels)

			transitionIdx, err := firstHybridToCELTFrameIndex(c)
			if err != nil {
				t.Fatalf("find transition: %v", err)
			}

			frameSamples := c.FrameSize * c.Channels
			cmp, err := transitionFrameComparison(refDecoded, gotDecoded, c.Channels, frameSamples, transitionIdx)
			if err != nil {
				t.Fatalf("transition frame quality: %v", err)
			}
			AssertQuality(t, cmp, tc.bar, fmt.Sprintf("%s transition frame=%d", tc.name, transitionIdx))

			// Score the following CELT frame independently too.
			if transitionIdx+1 < c.Frames {
				nextCmp, err := transitionFrameComparison(refDecoded, gotDecoded, c.Channels, frameSamples, transitionIdx+1)
				if err != nil {
					t.Fatalf("next frame quality: %v", err)
				}
				AssertQuality(t, nextCmp, transitionPostBar, fmt.Sprintf("%s post-transition frame=%d", tc.name, transitionIdx+1))
			}
		})
	}
}

func TestDecoderHybridToCELT20msTransitionParity(t *testing.T) {
	t.Parallel()
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
	refDecoded, err := decodeWithMatchedTierReferencePacketsSingle(fixture.SampleRate, c.Channels, c.FrameSize, packets)
	if err != nil {
		t.Fatalf("decode packets with matched libopus: %v", err)
	}
	gotDecoded := decodeWithInternalDecoder(t, packets, c.Channels)
	if len(gotDecoded) != len(refDecoded) {
		t.Fatalf("decoded length mismatch: Go=%d matched C=%d", len(gotDecoded), len(refDecoded))
	}

	assertTransitionSequenceExact(t, gotDecoded, refDecoded, c.FrameSize*c.Channels)

	transitionIdx, err := firstHybridToCELTFrameIndex(c)
	if err != nil {
		t.Fatalf("find transition: %v", err)
	}

	frameSamples := c.FrameSize * c.Channels
	cmp, err := transitionFrameComparison(refDecoded, gotDecoded, c.Channels, frameSamples, transitionIdx)
	if err != nil {
		t.Fatalf("transition frame quality: %v", err)
	}
	// 20ms transition frame: documented explicit bound (Q>=40; corr/RMS unchecked).
	transitionBar := QualityBar{MinQ: 40.0, Desc: "20ms hybrid->celt transition frame (Q>=40)"}
	AssertQuality(t, cmp, transitionBar, fmt.Sprintf("hybrid-fb-20ms-stereo-24k transition frame=%d", transitionIdx))

	if transitionIdx+1 < c.Frames {
		nextCmp, err := transitionFrameComparison(refDecoded, gotDecoded, c.Channels, frameSamples, transitionIdx+1)
		if err != nil {
			t.Fatalf("next frame quality: %v", err)
		}
		AssertQuality(t, nextCmp, transitionPostBar, fmt.Sprintf("hybrid-fb-20ms-stereo-24k post-transition frame=%d", transitionIdx+1))
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

func assertTransitionSequenceExact(t *testing.T, got, want []float32, frameSamples int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("PCM length=%d want=%d", len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("frame=%d sample=%d bits=%08x want=%08x", i/frameSamples, i%frameSamples, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}
