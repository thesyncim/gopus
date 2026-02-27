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
		"celt-fb-20ms-mono-64k-plc|burst2_mid":   {minQ: -55.0, minCorr: 0.992, minRMS: 0.97, maxRMS: 1.03},
		"celt-fb-20ms-mono-64k-plc|periodic9":    {minQ: -80.0, minCorr: 0.95, minRMS: 0.93, maxRMS: 1.03},
		"celt-fb-20ms-mono-64k-plc|single_mid":   {minQ: -68.0, minCorr: 0.98, minRMS: 0.96, maxRMS: 1.03},
		"hybrid-fb-20ms-mono-32k-fec|burst2_mid": {minQ: -68.0, minCorr: 0.98, minRMS: 0.98, maxRMS: 1.04},
		"hybrid-fb-20ms-mono-32k-fec|periodic9":  {minQ: -68.0, minCorr: 0.98, minRMS: 0.98, maxRMS: 1.04},
		"hybrid-fb-20ms-mono-32k-fec|single_mid": {minQ: 40.0, minCorr: 0.99, minRMS: 0.98, maxRMS: 1.02},
		"silk-wb-20ms-mono-24k-fec|burst2_mid":   {minQ: -68.0, minCorr: 0.98, minRMS: 0.97, maxRMS: 1.04},
		"silk-wb-20ms-mono-24k-fec|periodic9":    {minQ: -72.0, minCorr: 0.98, minRMS: 0.97, maxRMS: 1.04},
		"silk-wb-20ms-mono-24k-fec|single_mid":   {minQ: 60.0, minCorr: 0.99, minRMS: 0.98, maxRMS: 1.02},
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

func decoderLossStressThresholdForCase(c libopusDecoderLossCaseFile, pattern string) decoderLossThresholds {
	ratchet := map[string]decoderLossThresholds{
		"celt-fb-20ms-mono-64k-plc|burst6_mid": {minQ: 50.0, minCorr: 0.99, minRMS: 0.95, maxRMS: 1.05},
	}
	if thr, ok := ratchet[c.Name+"|"+pattern]; ok {
		return thr
	}

	// Stress patterns intentionally apply harsher and denser loss masks than the
	// baked fixture patterns; use dedicated floors to catch regressions without
	// forcing unrealistic parity under heavy concealment drift.
	switch {
	case strings.HasPrefix(c.Name, "hybrid-"):
		return decoderLossThresholds{minQ: -110.0, minCorr: 0.25, minRMS: 0.80, maxRMS: 3.20}
	case strings.HasPrefix(c.Name, "silk-"):
		return decoderLossThresholds{minQ: -110.0, minCorr: 0.30, minRMS: 0.60, maxRMS: 1.35}
	default:
		return decoderLossThresholds{minQ: -95.0, minCorr: 0.80, minRMS: 0.80, maxRMS: 1.20}
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

type decoderLossPattern struct {
	name string
	bits string
}

func buildDecoderLossStressPatterns(frames int) []decoderLossPattern {
	if frames < 4 {
		return nil
	}

	newBits := func() []byte {
		return bytes.Repeat([]byte{'0'}, frames)
	}
	markLoss := func(bits []byte, idx int) {
		if idx >= 0 && idx < len(bits) {
			bits[idx] = '1'
		}
	}
	finalize := func(name string, bits []byte) (decoderLossPattern, bool) {
		// Match fixture behavior: do not end the stream with a lost frame.
		bits[len(bits)-1] = '0'
		ones := 0
		for _, b := range bits {
			if b == '1' {
				ones++
			}
		}
		if ones == 0 {
			return decoderLossPattern{}, false
		}
		return decoderLossPattern{name: name, bits: string(bits)}, true
	}

	patterns := make([]decoderLossPattern, 0, 4)
	mid := frames / 2

	burst3 := newBits()
	markLoss(burst3, mid-1)
	markLoss(burst3, mid)
	markLoss(burst3, mid+1)
	if p, ok := finalize("burst3_mid", burst3); ok {
		patterns = append(patterns, p)
	}

	burst6 := newBits()
	markLoss(burst6, mid-2)
	markLoss(burst6, mid-1)
	markLoss(burst6, mid)
	markLoss(burst6, mid+1)
	markLoss(burst6, mid+2)
	markLoss(burst6, mid+3)
	if p, ok := finalize("burst6_mid", burst6); ok {
		patterns = append(patterns, p)
	}

	periodic5 := newBits()
	for i := 4; i < frames-1; i += 5 {
		markLoss(periodic5, i)
	}
	if p, ok := finalize("periodic5", periodic5); ok {
		patterns = append(patterns, p)
	}

	edgeThenMid := newBits()
	markLoss(edgeThenMid, 1)
	markLoss(edgeThenMid, mid)
	if p, ok := finalize("edge_then_mid", edgeThenMid); ok {
		patterns = append(patterns, p)
	}

	doubletStride7 := newBits()
	for i := 7; i < frames-2; i += 7 {
		markLoss(doubletStride7, i)
		markLoss(doubletStride7, i+1)
	}
	if p, ok := finalize("doublet_stride7", doubletStride7); ok {
		patterns = append(patterns, p)
	}

	return patterns
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

func TestDecoderLossStressPatternsAgainstOpusDemo(t *testing.T) {
	requireTestTier(t, testTierExhaustive)

	opusDemo, ok := getFixtureOpusDemoPath()
	if !ok {
		t.Skip("tmp_check opus_demo not found; skipping decoder loss stress parity check")
	}

	fixture, err := loadLibopusDecoderLossFixture()
	if err != nil {
		t.Fatalf("load decoder loss fixture: %v", err)
	}
	if fixture.SampleRate != 48000 {
		t.Fatalf("unsupported decoder loss fixture sample rate: %d", fixture.SampleRate)
	}

	tmpDir, err := os.MkdirTemp("", "gopus-decoder-loss-stress-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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

			bitstream, err := buildDecoderLossBitstream(c)
			if err != nil {
				t.Fatalf("build fixture bitstream: %v", err)
			}
			bitPath := filepath.Join(tmpDir, fmt.Sprintf("%s.stress.bit", c.Name))
			if err := os.WriteFile(bitPath, bitstream, 0o644); err != nil {
				t.Fatalf("write bitstream: %v", err)
			}

			patterns := buildDecoderLossStressPatterns(c.Frames)
			if len(patterns) == 0 {
				t.Fatalf("no stress loss patterns generated for %d frames", c.Frames)
			}

			for _, p := range patterns {
				p := p
				t.Run(p.name, func(t *testing.T) {
					lossPath := filepath.Join(tmpDir, fmt.Sprintf("%s.%s.loss", c.Name, p.name))
					outPath := filepath.Join(tmpDir, fmt.Sprintf("%s.%s.f32", c.Name, p.name))
					if err := writeLossBitsFile(lossPath, p.bits); err != nil {
						t.Fatalf("write lossfile: %v", err)
					}

					cmd := exec.Command(
						opusDemo, "-d", "48000", fmt.Sprintf("%d", c.Channels),
						"-f32", "-lossfile", lossPath, bitPath, outPath,
					)
					cmdOut, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("opus_demo decode failed: %v (%s)", err, cmdOut)
					}

					refRaw, err := os.ReadFile(outPath)
					if err != nil {
						t.Fatalf("read opus_demo output: %v", err)
					}
					refDecoded, err := decodeRawFloat32LE(refRaw)
					if err != nil {
						t.Fatalf("decode opus_demo output: %v", err)
					}

					gotDecoded := decodeWithInternalDecoderLossPattern(
						t,
						fixture.SampleRate,
						c.Channels,
						packets,
						parseLossBits(p.bits),
					)
					if len(refDecoded) == 0 || len(gotDecoded) == 0 {
						t.Fatalf("decoded streams empty: ref=%d got=%d", len(refDecoded), len(gotDecoded))
					}

					maxLenDrift := c.FrameSize * c.Channels
					if d := lossAbsInt(len(refDecoded) - len(gotDecoded)); d > maxLenDrift {
						t.Fatalf("decoded length drift too large: ref=%d got=%d drift=%d max=%d",
							len(refDecoded), len(gotDecoded), d, maxLenDrift)
					}

					compareLen := len(refDecoded)
					if len(gotDecoded) < compareLen {
						compareLen = len(gotDecoded)
					}
					thr := decoderLossStressThresholdForCase(c, p.name)
					maxDelay := 4 * c.FrameSize
					if maxDelay < 960 {
						maxDelay = 960
					}
					q, delay := ComputeQualityFloat32WithDelay(refDecoded[:compareLen], gotDecoded[:compareLen], fixture.SampleRate, maxDelay)
					corr, rmsRatio := decoderParityStats(refDecoded[:compareLen], gotDecoded[:compareLen])
					t.Logf("Q=%.2f SNR=%.2f delay=%d corr=%.6f rms_ratio=%.6f len_ref=%d len_got=%d",
						q, SNRFromQuality(q), delay, corr, rmsRatio, len(refDecoded), len(gotDecoded))

					// FEC/PLC stress parity is highly delay-sensitive on SILK masks.
					// Keep all SILK FEC stress lanes locked to near-zero delay drift
					// against opus_demo.
					if strings.HasPrefix(c.Name, "silk-") && strings.Contains(c.Name, "-fec") {
						if d := lossAbsInt(delay); d > 1 {
							t.Fatalf("decoder loss stress delay regression: |delay|=%d > 1", d)
						}
					}

					if q < thr.minQ {
						t.Fatalf("decoder loss stress parity quality regression: Q=%.2f < %.2f", q, thr.minQ)
					}
					if corr < thr.minCorr {
						t.Fatalf("decoder loss stress parity correlation regression: corr=%.6f < %.6f", corr, thr.minCorr)
					}
					if rmsRatio < thr.minRMS || rmsRatio > thr.maxRMS {
						t.Fatalf("decoder loss stress parity RMS ratio regression: ratio=%.6f outside [%.6f, %.6f]",
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
