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
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

type decoderParityThresholds struct {
	minQ    float64
	minCorr float64
	minRMS  float64
	maxRMS  float64
}

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

func decoderParityThresholdForCase(c libopusDecoderMatrixCaseFile) decoderParityThresholds {
	if strings.HasPrefix(c.Name, "hybrid-") || c.ModeHistogram["hybrid"] > 0 {
		// Hybrid decoding parity is currently looser than SILK/CELT; keep
		// this strict enough for non-regression while allowing active tuning.
		if c.Channels == 2 {
			return decoderParityThresholds{minQ: -65.0, minCorr: 0.990, minRMS: 0.97, maxRMS: 1.03}
		}
		return decoderParityThresholds{minQ: -72.0, minCorr: 0.985, minRMS: 0.97, maxRMS: 1.03}
	}

	mode := decoderDominantMode(c.ModeHistogram)
	switch mode {
	case "silk":
		return decoderParityThresholds{minQ: 45.0, minCorr: 0.997, minRMS: 0.98, maxRMS: 1.02}
	case "celt":
		return decoderParityThresholds{minQ: 45.0, minCorr: 0.998, minRMS: 0.98, maxRMS: 1.02}
	default:
		return decoderParityThresholds{minQ: -72.0, minCorr: 0.985, minRMS: 0.97, maxRMS: 1.03}
	}
}

func decoderParityStats(a, b []float32) (corr, rmsRatio float64) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0, 0
	}
	var sumA, sumB, sumASq, sumBSq float64
	for i := 0; i < n; i++ {
		fa := float64(a[i])
		fb := float64(b[i])
		sumA += fa
		sumB += fb
		sumASq += fa * fa
		sumBSq += fb * fb
	}
	meanA := sumA / float64(n)
	meanB := sumB / float64(n)
	var cov, varA, varB float64
	for i := 0; i < n; i++ {
		da := float64(a[i]) - meanA
		db := float64(b[i]) - meanB
		cov += da * db
		varA += da * da
		varB += db * db
	}
	if varA > 0 && varB > 0 {
		corr = cov / math.Sqrt(varA*varB)
	}
	rmsA := math.Sqrt(sumASq / float64(n))
	rmsB := math.Sqrt(sumBSq / float64(n))
	if rmsA > 0 {
		rmsRatio = rmsB / rmsA
	}
	return corr, rmsRatio
}

func TestDecoderParityLibopusMatrix(t *testing.T) {
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
		c := c
		t.Run(c.Name, func(t *testing.T) {
			thr := decoderParityThresholdForCase(c)
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

			compareLen := len(refDecoded)
			if len(internalDecoded) < compareLen {
				compareLen = len(internalDecoded)
			}
			maxDelay := 4 * c.FrameSize
			if maxDelay < 960 {
				maxDelay = 960
			}
			q, delay := ComputeQualityFloat32WithDelay(refDecoded[:compareLen], internalDecoded[:compareLen], fixture.SampleRate, maxDelay)
			corr, rmsRatio := decoderParityStats(refDecoded[:compareLen], internalDecoded[:compareLen])
			t.Logf("Q=%.2f SNR=%.2f delay=%d corr=%.6f rms_ratio=%.6f", q, SNRFromQuality(q), delay, corr, rmsRatio)

			if q < thr.minQ {
				t.Fatalf("decoder parity quality regression: Q=%.2f < %.2f", q, thr.minQ)
			}
			if corr < thr.minCorr {
				t.Fatalf("decoder parity correlation regression: corr=%.6f < %.6f", corr, thr.minCorr)
			}
			if rmsRatio < thr.minRMS || rmsRatio > thr.maxRMS {
				t.Fatalf("decoder parity RMS ratio regression: ratio=%.6f outside [%.6f, %.6f]", rmsRatio, thr.minRMS, thr.maxRMS)
			}
		})
	}
}

func TestDecoderParityMatrixCoverage(t *testing.T) {
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

func TestDecoderParityMatrixFixtureHonestyWithOpusDemo(t *testing.T) {
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
		c := c
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
				t.Fatalf("fixture drift vs tmp_check opus_demo %s: got=%d bytes want=%d bytes", libopustooling.DefaultVersion, len(gotRaw), len(wantRaw))
			}
		})
	}
}
