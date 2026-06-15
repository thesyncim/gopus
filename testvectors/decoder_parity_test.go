package testvectors

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func decoderDominantMode(hist map[string]int) string {
	bestMode := "unknown"
	bestCount := -1
	for _, mode := range []string{"silk", "hybrid", "celt"} {
		if count := hist[mode]; count > bestCount {
			bestMode = mode
			bestCount = count
		}
	}
	return bestMode
}

// decoderMatrixCaseMode maps a decoder-matrix case to its dominant codec mode,
// which selects the trusted QualityBar (see qualitycompare.go QualityBarForMode).
// Hybrid is now held to the same near-exact bar as SILK/CELT (measured Q>=99.7,
// corr=1.0 vs libopus across the FB/SWB hybrid matrix).
func decoderMatrixCaseMode(c libopusDecoderMatrixCaseFile) string {
	if strings.HasPrefix(c.Name, "hybrid-") || c.ModeHistogram["hybrid"] > 0 {
		return "hybrid"
	}
	return decoderDominantMode(c.ModeHistogram)
}

func TestDecoderParityLibopusMatrix(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)

	fixture, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder matrix fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported decoder matrix fixture version: %d", fixture.Version)
	}
	if fixture.SampleRate != 48000 {
		t.Fatalf("unsupported sample rate: %d", fixture.SampleRate)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("decoder matrix fixture has no cases")
	}

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			bar := QualityBarForMode(decoderMatrixCaseMode(c), c.Channels)
			packets, err := decodeLibopusDecoderMatrixPackets(c)
			if err != nil {
				t.Fatalf("decode fixture packets: %v", err)
			}
			refDecoded, err := decodeLibopusDecoderMatrixSamples(c)
			if err != nil {
				t.Fatalf("decode fixture f32 samples: %v", err)
			}
			internalDecoded := decodeWithInternalDecoder(t, packets, c.Channels)
			if len(refDecoded) == 0 || len(internalDecoded) == 0 {
				t.Fatalf("decoded streams empty: ref=%d internal=%d", len(refDecoded), len(internalDecoded))
			}

			compareLen := min(len(internalDecoded), len(refDecoded))
			maxDelay := max(4*c.FrameSize, 960)
			cmp, err := CompareDecodedFloat32(internalDecoded[:compareLen], refDecoded[:compareLen], fixture.SampleRate, c.Channels, maxDelay)
			if err != nil {
				t.Fatalf("compare decoded quality: %v", err)
			}
			AssertQuality(t, cmp, bar, c.Name)
		})
	}
}

func TestDecoderParityMatrixCoverage(t *testing.T) {
	t.Parallel()
	fixture, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder matrix fixture: %v", err)
	}
	seenModes := map[string]bool{"silk": false, "hybrid": false, "celt": false}
	seenStereo := false
	seenLongFrame := false
	for _, c := range fixture.Cases {
		if c.Channels == 2 {
			seenStereo = true
		}
		if c.FrameSize >= 960 {
			seenLongFrame = true
		}
		for mode, count := range c.ModeHistogram {
			if count > 0 {
				seenModes[mode] = true
			}
		}
	}
	var missing []string
	for _, mode := range []string{"silk", "hybrid", "celt"} {
		if !seenModes[mode] {
			missing = append(missing, mode)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("decoder matrix missing mode coverage: %v", missing)
	}
	if !seenStereo {
		t.Fatal("decoder matrix missing stereo coverage")
	}
	if !seenLongFrame {
		t.Fatal("decoder matrix missing >=20ms frame coverage")
	}
}

func buildOpusDemoBitstreamFromFixtureCase(c libopusDecoderMatrixCaseFile) ([]byte, error) {
	packets, err := decodeLibopusDecoderMatrixPackets(c)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, p := range packets {
		total += 8 + len(p)
	}
	out := make([]byte, 0, total)
	lenField := make([]byte, 4)
	rangeField := make([]byte, 4)
	for i, p := range packets {
		binary.BigEndian.PutUint32(lenField, uint32(len(p)))
		binary.BigEndian.PutUint32(rangeField, c.Packets[i].FinalRange)
		out = append(out, lenField...)
		out = append(out, rangeField...)
		out = append(out, p...)
	}
	return out, nil
}

func getFixtureOpusDemoPath() (string, bool) {
	return libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
}

func decodeRawFloat32LE(raw []byte) ([]float32, error) {
	if len(raw)%4 != 0 {
		return nil, fmt.Errorf("raw f32 payload length must be multiple of 4, got %d", len(raw))
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return out, nil
}

func TestDecoderParityMatrixFixtureHonestyWithOpusDemo(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierExhaustive)

	opusDemo, ok := getFixtureOpusDemoPath()
	if !ok {
		t.Skip("tmp_check opus_demo not found; skipping fixture honesty check")
	}
	fixture, err := loadLibopusDecoderMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder matrix fixture: %v", err)
	}
	tmpDir, err := os.MkdirTemp("", "gopus-fixture-honesty-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			bitstream, err := buildOpusDemoBitstreamFromFixtureCase(c)
			if err != nil {
				t.Fatalf("build fixture bitstream: %v", err)
			}
			bitPath := filepath.Join(tmpDir, fmt.Sprintf("%s.bit", c.Name))
			outPath := filepath.Join(tmpDir, fmt.Sprintf("%s.f32", c.Name))
			if err := os.WriteFile(bitPath, bitstream, 0o644); err != nil {
				t.Fatalf("write bitstream: %v", err)
			}
			cmd := exec.Command(opusDemo, "-d", "48000", fmt.Sprintf("%d", c.Channels), "-f32", bitPath, outPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("opus_demo decode failed: %v (%s)", err, out)
			}
			gotRaw, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("read opus_demo output: %v", err)
			}
			wantRaw, err := base64.StdEncoding.DecodeString(c.DecodedF32B64)
			if err != nil {
				t.Fatalf("decode fixture decoded payload: %v", err)
			}
			if !bytes.Equal(gotRaw, wantRaw) {
				if runtime.GOARCH == "amd64" {
					// Native amd64 libopus decode can drift at sample-bit level across toolchains.
					// Keep a strict waveform guard to catch true regressions.
					gotSamples, err := decodeRawFloat32LE(gotRaw)
					if err != nil {
						t.Fatalf("decode live decoded payload: %v", err)
					}
					wantSamples, err := decodeRawFloat32LE(wantRaw)
					if err != nil {
						t.Fatalf("decode fixture decoded payload: %v", err)
					}
					q, delay, err := computeOpusCompareQualityBetweenDecoded(wantSamples, gotSamples, 48000, c.Channels, amd64FixtureWaveformMaxDelay)
					if err != nil {
						t.Fatalf("compute fixture opus_compare quality on amd64: %v", err)
					}
					if q < amd64FixtureWaveformMinQ {
						t.Fatalf("fixture drift vs tmp_check opus_demo %s on amd64: Q=%.2f delay=%d (got=%d bytes want=%d bytes)", libopustooling.DefaultVersion, q, delay, len(gotRaw), len(wantRaw))
					}
					t.Logf("non-bitexact decoder drift on amd64 accepted: Q=%.2f delay=%d", q, delay)
					return
				}
				t.Fatalf("fixture drift vs tmp_check opus_demo %s: got=%d bytes want=%d bytes", libopustooling.DefaultVersion, len(gotRaw), len(wantRaw))
			}
		})
	}
}
