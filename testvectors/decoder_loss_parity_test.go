package testvectors

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus"
)

type decoderLossThresholds struct {
	minQ    float64
	minCorr float64
	minRMS  float64
	maxRMS  float64
}

func decoderLossThresholdForCase(c libopusDecoderLossCaseFile, pattern string) decoderLossThresholds {
	// These are parity ratchet floors observed against the pinned fixture set.
	// They intentionally track current behavior and prevent regressions while
	// FEC/PLC quality parity is still being improved.
	ratchet := map[string]decoderLossThresholds{
		"celt-fb-20ms-mono-64k-plc|burst2_mid":   {minQ: -55.0, minCorr: 0.99, minRMS: 0.95, maxRMS: 1.05},
		"celt-fb-20ms-mono-64k-plc|periodic9":    {minQ: -82.0, minCorr: 0.94, minRMS: 0.90, maxRMS: 1.06},
		"celt-fb-20ms-mono-64k-plc|single_mid":   {minQ: -70.0, minCorr: 0.97, minRMS: 0.95, maxRMS: 1.05},
		"hybrid-fb-20ms-mono-32k-fec|burst2_mid": {minQ: -98.0, minCorr: 0.65, minRMS: 1.35, maxRMS: 1.65},
		"hybrid-fb-20ms-mono-32k-fec|periodic9":  {minQ: -103.0, minCorr: 0.45, minRMS: 1.30, maxRMS: 1.65},
		"hybrid-fb-20ms-mono-32k-fec|single_mid": {minQ: -88.0, minCorr: 0.88, minRMS: 0.90, maxRMS: 1.12},
		"silk-wb-20ms-mono-24k-fec|burst2_mid":   {minQ: -70.0, minCorr: 0.96, minRMS: 0.90, maxRMS: 1.10},
		"silk-wb-20ms-mono-24k-fec|periodic9":    {minQ: -104.0, minCorr: 0.38, minRMS: 1.25, maxRMS: 1.55},
		"silk-wb-20ms-mono-24k-fec|single_mid":   {minQ: -90.0, minCorr: 0.88, minRMS: 0.85, maxRMS: 1.05},
	}
	if thr, ok := ratchet[c.Name+"|"+pattern]; ok {
		return thr
	}

	switch {
	case strings.HasPrefix(c.Name, "silk-"), strings.HasPrefix(c.Name, "hybrid-"):
		return decoderLossThresholds{minQ: -110.0, minCorr: 0.35, minRMS: 0.70, maxRMS: 1.80}
	default:
		return decoderLossThresholds{minQ: -95.0, minCorr: 0.50, minRMS: 0.70, maxRMS: 1.50}
	}
}

func decodeWithInternalDecoderLossPattern(
	t *testing.T,
	sampleRate int,
	channels int,
	packets [][]byte,
	loss []bool,
) []float32 {
	t.Helper()

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("create decoder: %v", err)
	}

	outBuf := make([]float32, 5760*channels)
	var decoded []float32
	lostCount := 0

	for i := range packets {
		if i < len(loss) && loss[i] {
			lostCount++
			continue
		}

		runDecoder := 1
		if lostCount > 0 {
			runDecoder += lostCount
		}

		for fr := 0; fr < runDecoder; fr++ {
			var (
				n   int
				err error
			)
			switch {
			case fr == lostCount-1 && lostCount > 0:
				n, err = dec.DecodeWithFEC(packets[i], outBuf, true)
			case fr < lostCount:
				n, err = dec.Decode(nil, outBuf)
			default:
				n, err = dec.Decode(packets[i], outBuf)
			}
			if err != nil {
				t.Fatalf("decode failure at packet=%d fr=%d lostCount=%d: %v", i, fr, lostCount, err)
			}
			if n > 0 {
				decoded = append(decoded, outBuf[:n*channels]...)
			}
		}

		lostCount = 0
	}

	// Match opus_demo behavior: do not synthesize trailing losses once stream ends.
	return decoded
}

func lossAbsInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestDecoderLossParityLibopusFixture(t *testing.T) {
	requireTestTier(t, testTierParity)

	fixture, err := loadLibopusDecoderLossFixture()
	if err != nil {
		t.Fatalf("load decoder loss fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported decoder loss fixture version: %d", fixture.Version)
	}
	if fixture.SampleRate != 48000 {
		t.Fatalf("unsupported decoder loss fixture sample rate: %d", fixture.SampleRate)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("decoder loss fixture has no cases")
	}

	for _, c := range fixture.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			packets, err := decodeLibopusDecoderLossPackets(c)
			if err != nil {
				t.Fatalf("decode fixture packets: %v", err)
			}
			if len(packets) == 0 {
				t.Fatal("fixture case has no packets")
			}

			for _, r := range c.Results {
				r := r
				t.Run(r.Pattern, func(t *testing.T) {
					refDecoded, err := decodeLibopusDecoderLossSamples(r)
					if err != nil {
						t.Fatalf("decode fixture reference samples: %v", err)
					}
					gotDecoded := decodeWithInternalDecoderLossPattern(
						t,
						fixture.SampleRate,
						c.Channels,
						packets,
						parseLossBits(r.LossBits),
					)
					if len(refDecoded) == 0 || len(gotDecoded) == 0 {
						t.Fatalf("decoded streams empty: ref=%d got=%d", len(refDecoded), len(gotDecoded))
					}

					// Both sides follow the same decode cadence. Allow <=1 frame drift.
					maxLenDrift := c.FrameSize * c.Channels
					if d := lossAbsInt(len(refDecoded) - len(gotDecoded)); d > maxLenDrift {
						t.Fatalf("decoded length drift too large: ref=%d got=%d drift=%d max=%d",
							len(refDecoded), len(gotDecoded), d, maxLenDrift)
					}

					compareLen := len(refDecoded)
					if len(gotDecoded) < compareLen {
						compareLen = len(gotDecoded)
					}
					thr := decoderLossThresholdForCase(c, r.Pattern)
					maxDelay := 4 * c.FrameSize
					if maxDelay < 960 {
						maxDelay = 960
					}
					q, delay := ComputeQualityFloat32WithDelay(refDecoded[:compareLen], gotDecoded[:compareLen], fixture.SampleRate, maxDelay)
					corr, rmsRatio := decoderParityStats(refDecoded[:compareLen], gotDecoded[:compareLen])
					t.Logf("Q=%.2f SNR=%.2f delay=%d corr=%.6f rms_ratio=%.6f len_ref=%d len_got=%d",
						q, SNRFromQuality(q), delay, corr, rmsRatio, len(refDecoded), len(gotDecoded))

					if q < thr.minQ {
						t.Fatalf("decoder loss parity quality regression: Q=%.2f < %.2f", q, thr.minQ)
					}
					if corr < thr.minCorr {
						t.Fatalf("decoder loss parity correlation regression: corr=%.6f < %.6f", corr, thr.minCorr)
					}
					if rmsRatio < thr.minRMS || rmsRatio > thr.maxRMS {
						t.Fatalf("decoder loss parity RMS ratio regression: ratio=%.6f outside [%.6f, %.6f]",
							rmsRatio, thr.minRMS, thr.maxRMS)
					}
				})
			}
		})
	}
}

func buildDecoderLossBitstream(c libopusDecoderLossCaseFile) ([]byte, error) {
	packets, err := decodeLibopusDecoderLossPackets(c)
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

func writeLossBitsFile(path, bits string) error {
	buf := make([]byte, 0, len(bits)*2)
	for i := 0; i < len(bits); i++ {
		buf = append(buf, bits[i], '\n')
	}
	return os.WriteFile(path, buf, 0o644)
}

func TestDecoderLossFixtureHonestyWithOpusDemo(t *testing.T) {
	requireTestTier(t, testTierExhaustive)

	opusDemo, ok := getFixtureOpusDemoPath()
	if !ok {
		t.Skip("tmp_check opus_demo not found; skipping decoder loss fixture honesty check")
	}

	fixture, err := loadLibopusDecoderLossFixture()
	if err != nil {
		t.Fatalf("load decoder loss fixture: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "gopus-decoder-loss-honesty-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	for _, c := range fixture.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			bitstream, err := buildDecoderLossBitstream(c)
			if err != nil {
				t.Fatalf("build fixture bitstream: %v", err)
			}
			bitPath := filepath.Join(tmpDir, fmt.Sprintf("%s.bit", c.Name))
			if err := os.WriteFile(bitPath, bitstream, 0o644); err != nil {
				t.Fatalf("write bitstream: %v", err)
			}

			for _, r := range c.Results {
				r := r
				t.Run(r.Pattern, func(t *testing.T) {
					lossPath := filepath.Join(tmpDir, fmt.Sprintf("%s.%s.loss", c.Name, r.Pattern))
					outPath := filepath.Join(tmpDir, fmt.Sprintf("%s.%s.f32", c.Name, r.Pattern))
					if err := writeLossBitsFile(lossPath, r.LossBits); err != nil {
						t.Fatalf("write lossfile: %v", err)
					}

					cmd := exec.Command(
						opusDemo, "-d", "48000", fmt.Sprintf("%d", c.Channels),
						"-f32", "-lossfile", lossPath, bitPath, outPath,
					)
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("opus_demo decode failed: %v (%s)", err, out)
					}

					gotRaw, err := os.ReadFile(outPath)
					if err != nil {
						t.Fatalf("read opus_demo output: %v", err)
					}
					wantRaw, err := base64.StdEncoding.DecodeString(r.DecodedF32B64)
					if err != nil {
						t.Fatalf("decode fixture decoded payload: %v", err)
					}

					if !bytes.Equal(gotRaw, wantRaw) {
						if runtime.GOARCH == "amd64" {
							gotSamples, err := decodeRawFloat32LE(gotRaw)
							if err != nil {
								t.Fatalf("decode live payload: %v", err)
							}
							wantSamples, err := decodeRawFloat32LE(wantRaw)
							if err != nil {
								t.Fatalf("decode fixture payload: %v", err)
							}
							q, _ := ComputeQualityFloat32WithDelay(wantSamples, gotSamples, 48000, 960)
							if q < 35.0 {
								t.Fatalf("decoder loss fixture drift on amd64: Q=%.2f (got=%d bytes want=%d bytes)",
									q, len(gotRaw), len(wantRaw))
							}
							t.Logf("non-bitexact decoder loss drift on amd64 accepted: Q=%.2f", q)
							return
						}
						t.Fatalf("decoder loss fixture drift: got=%d bytes want=%d bytes", len(gotRaw), len(wantRaw))
					}
				})
			}
		})
	}
}
