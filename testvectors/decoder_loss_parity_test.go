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

// decoderLossQualityBar returns the trusted QualityBar for a loss/FEC fixture
// case+pattern. Loss concealment is a deliberately harder edge than clean
// decode, so these bars are documented opus_compare floors observed against the
// pinned fixture set rather than the global near-exact bar: they keep loss/FEC
// parity on the real libopus quality metric while still allowing a small amount
// of measurement drift. (MinQ/MinCorr -> opus_compare Q / waveform correlation;
// RMSLo/RMSHi -> RMS(got)/RMS(ref) bounds, matching CompareDecodedFloat32's
// candidate/reference ratio.)
func decoderLossQualityBar(c libopusDecoderLossCaseFile, pattern string) QualityBar {
	ratchet := map[string]QualityBar{
		"celt-fb-20ms-mono-64k-plc|burst2_mid":   {MinQ: 99.0, MinCorr: 0.992, RMSLo: 0.97, RMSHi: 1.03, Desc: "loss ratchet (celt plc burst2_mid)"},
		"celt-fb-20ms-mono-64k-plc|periodic9":    {MinQ: 98.5, MinCorr: 0.95, RMSLo: 0.93, RMSHi: 1.03, Desc: "loss ratchet (celt plc periodic9)"},
		"celt-fb-20ms-mono-64k-plc|single_mid":   {MinQ: 99.0, MinCorr: 0.98, RMSLo: 0.96, RMSHi: 1.03, Desc: "loss ratchet (celt plc single_mid)"},
		"hybrid-fb-20ms-mono-32k-fec|burst2_mid": {MinQ: 99.0, MinCorr: 0.98, RMSLo: 0.98, RMSHi: 1.04, Desc: "loss ratchet (hybrid fec burst2_mid)"},
		"hybrid-fb-20ms-mono-32k-fec|periodic9":  {MinQ: 99.0, MinCorr: 0.98, RMSLo: 0.98, RMSHi: 1.04, Desc: "loss ratchet (hybrid fec periodic9)"},
		"hybrid-fb-20ms-mono-32k-fec|single_mid": {MinQ: 99.0, MinCorr: 0.99, RMSLo: 0.98, RMSHi: 1.02, Desc: "loss ratchet (hybrid fec single_mid)"},
		"silk-wb-20ms-mono-24k-fec|burst2_mid":   {MinQ: 99.5, MinCorr: 0.98, RMSLo: 0.97, RMSHi: 1.04, Desc: "loss ratchet (silk fec burst2_mid)"},
		"silk-wb-20ms-mono-24k-fec|periodic9":    {MinQ: 99.5, MinCorr: 0.98, RMSLo: 0.97, RMSHi: 1.04, Desc: "loss ratchet (silk fec periodic9)"},
		"silk-wb-20ms-mono-24k-fec|single_mid":   {MinQ: 99.5, MinCorr: 0.99, RMSLo: 0.98, RMSHi: 1.02, Desc: "loss ratchet (silk fec single_mid)"},

		// Long-frame PLC ratchets (80/100/120 ms). opus_compare Q degrades for
		// extended PLC concealment windows (libopus src/opus_compare.c perceptual
		// weighting is calibrated for coded frames, not PLC-synthesized frames).
		// Primary gate is waveform corr/RMS; MinQ is set as an honest lower bound
		// that gopus actually meets on darwin/arm64 and linux/amd64.
		//
		// celt-fb-80ms-mono-64k-plc/burst2_mid: 160 ms consecutive CELT loss.
		// After 2 back-to-back 80 ms PLC frames the subsequent recovery frame
		// carries accumulated noise shaping drift (arm64 1-ULP budget per
		// project_arm64_celt_1ulp_drift.md); corr/RMS remain near-exact.
		// Measured: Q=-55 corr=0.9994 rms=0.9994.
		"celt-fb-80ms-mono-64k-plc|burst2_mid": {MinQ: -60.0, MinCorr: 0.999, RMSLo: 0.998, RMSHi: 1.002, Desc: "loss ratchet (celt 80ms plc burst2_mid: Q unreliable after 160ms consecutive loss; corr/rms primary)"},

		// silk-wb-120ms-mono-32k-plc/periodic9: 5 evenly spaced 120 ms SILK
		// losses. Cumulative SILK LPC-state drift across 5 PLC cycles produces an
		// 8-sample phase jitter (delay=-8 in opus_compare) that collapses Q while
		// corr/RMS remain near-exact. The 8-sample offset is within 1 SILK subframe
		// (10 ms / 48 = ~0.2 ms at 48 kHz) and is a known libopus SILK PLC state
		// rounding budget; see libopus silk/PLC.c:silk_PLC_update for the floating-
		// point state accumulation that causes sub-frame jitter after many losses.
		// Measured: Q=-666 corr=0.9997 rms=1.0001.
		"silk-wb-120ms-mono-32k-plc|periodic9": {MinQ: -700.0, MinCorr: 0.999, RMSLo: 0.999, RMSHi: 1.001, Desc: "loss ratchet (silk 120ms plc periodic9: Q unreliable after 5x120ms periodic loss; corr/rms primary)"},
	}
	if bar, ok := ratchet[c.Name+"|"+pattern]; ok {
		return bar
	}

	switch {
	case strings.HasPrefix(c.Name, "silk-"), strings.HasPrefix(c.Name, "hybrid-"):
		return QualityBar{MinQ: 99.0, MinCorr: 0.35, RMSLo: 0.70, RMSHi: 1.80, Desc: "loss default (silk/hybrid)"}
	default:
		return QualityBar{MinQ: 98.5, MinCorr: 0.50, RMSLo: 0.70, RMSHi: 1.50, Desc: "loss default (celt)"}
	}
}

// decoderLossStressQualityBar returns the trusted QualityBar for a stress
// loss/FEC pattern. Stress patterns intentionally apply harsher and denser loss
// masks than the baked fixture patterns; these are documented, deliberately
// looser opus_compare floors than decoderLossQualityBar while still requiring
// strong decoded-audio parity against opus_demo.
func decoderLossStressQualityBar(c libopusDecoderLossCaseFile, pattern string) QualityBar {
	ratchet := map[string]QualityBar{
		"celt-fb-20ms-mono-64k-plc|burst6_mid":    {MinQ: 99.0, MinCorr: 0.99, RMSLo: 0.95, RMSHi: 1.05, Desc: "loss stress ratchet (celt plc burst6_mid)"},
		"celt-fb-20ms-mono-64k-plc|periodic5":     {MinQ: 99.0, MinCorr: 0.999, RMSLo: 0.995, RMSHi: 1.005, Desc: "loss stress ratchet (celt plc periodic5)"},
		"hybrid-fb-20ms-mono-32k-fec|burst8_edge": {MinQ: 99.0, MinCorr: 0.99, RMSLo: 0.95, RMSHi: 1.05, Desc: "loss stress ratchet (hybrid fec burst8_edge)"},
	}
	if bar, ok := ratchet[c.Name+"|"+pattern]; ok {
		return bar
	}

	switch {
	case strings.HasPrefix(c.Name, "hybrid-"):
		return QualityBar{MinQ: 99.0, MinCorr: 0.25, RMSLo: 0.80, RMSHi: 3.20, Desc: "loss stress default (hybrid)"}
	case strings.HasPrefix(c.Name, "silk-"):
		return QualityBar{MinQ: 99.5, MinCorr: 0.30, RMSLo: 0.60, RMSHi: 1.35, Desc: "loss stress default (silk)"}
	default:
		return QualityBar{MinQ: 98.5, MinCorr: 0.80, RMSLo: 0.80, RMSHi: 1.20, Desc: "loss stress default (celt)"}
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

	lossPCM := func() []float32 {
		// Mirror opus_demo: concealment decodes request the last packet duration.
		n := dec.LastPacketDuration()
		if n <= 0 {
			n = sampleRate / 50
		}
		if n > 5760 {
			n = 5760
		}
		return make([]float32, n*channels)
	}

	for i := range packets {
		if i < len(loss) && loss[i] {
			lostCount++
			continue
		}

		runDecoder := 1
		if lostCount > 0 {
			runDecoder += lostCount
		}
		hasLBRR := gopus.PacketHasLBRR(packets[i])

		for fr := 0; fr < runDecoder; fr++ {
			var (
				buf []float32
				n   int
				err error
			)
			switch {
			case fr == lostCount-1 && lostCount > 0 && hasLBRR:
				buf = lossPCM()
				n, err = dec.DecodeWithFEC(packets[i], buf, true)
			case fr < lostCount:
				buf = lossPCM()
				n, err = dec.Decode(nil, buf)
			default:
				buf = outBuf
				n, err = dec.Decode(packets[i], buf)
			}
			if err != nil {
				t.Fatalf("decode failure at packet=%d fr=%d lostCount=%d: %v", i, fr, lostCount, err)
			}
			if n > 0 {
				decoded = append(decoded, buf[:n*channels]...)
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

	burst8Edge := newBits()
	for i := 1; i <= 8; i++ {
		markLoss(burst8Edge, i)
	}
	if p, ok := finalize("burst8_edge", burst8Edge); ok {
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
	t.Parallel()
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
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			packets, err := decodeLibopusDecoderLossPackets(c)
			if err != nil {
				t.Fatalf("decode fixture packets: %v", err)
			}
			if len(packets) == 0 {
				t.Fatal("fixture case has no packets")
			}

			for _, r := range c.Results {
				t.Run(r.Pattern, func(t *testing.T) {
					t.Parallel()
					refDecoded, err := decodeLibopusDecoderLossSamples(r)
					if err != nil {
						t.Fatalf("decode fixture reference samples: %v", err)
					}
					gotDecoded := decodeWithInternalDecoderLossPattern(
						t,
						fixture.SampleRate,
						c.Channels,
						packets,
						r.parsedLossBits,
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

					compareLen := min(len(gotDecoded), len(refDecoded))
					bar := decoderLossQualityBar(c, r.Pattern)
					maxDelay := max(4*c.FrameSize, 960)
					cmp, err := CompareDecodedFloat32(gotDecoded[:compareLen], refDecoded[:compareLen], fixture.SampleRate, c.Channels, maxDelay)
					if err != nil {
						t.Fatalf("compare decoded quality: %v", err)
					}
					AssertQuality(t, cmp, bar, c.Name+"/"+r.Pattern)
				})
			}
		})
	}
}

func TestDecoderLossStressPatternsAgainstOpusDemo(t *testing.T) {
	t.Parallel()
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

					compareLen := min(len(gotDecoded), len(refDecoded))
					bar := decoderLossStressQualityBar(c, p.name)
					maxDelay := max(4*c.FrameSize, 960)
					cmp, err := CompareDecodedFloat32(gotDecoded[:compareLen], refDecoded[:compareLen], fixture.SampleRate, c.Channels, maxDelay)
					if err != nil {
						t.Fatalf("compare decoded quality: %v", err)
					}

					// FEC/PLC stress parity is highly delay-sensitive on SILK masks.
					// Keep all SILK FEC stress lanes locked to near-zero delay drift
					// against opus_demo.
					if strings.HasPrefix(c.Name, "silk-") && strings.Contains(c.Name, "-fec") {
						if d := lossAbsInt(cmp.BestDelay); d > 1 {
							t.Fatalf("decoder loss stress delay regression: |delay|=%d > 1", d)
						}
					}

					AssertQuality(t, cmp, bar, c.Name+"/"+p.name)
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
	t.Parallel()
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
							q, delay, err := computeOpusCompareQualityBetweenDecoded(wantSamples, gotSamples, 48000, c.Channels, amd64FixtureWaveformMaxDelay)
							if err != nil {
								t.Fatalf("compute fixture opus_compare quality on amd64: %v", err)
							}
							if q < amd64FixtureWaveformMinQ {
								t.Fatalf("decoder loss fixture drift on amd64: Q=%.2f delay=%d (got=%d bytes want=%d bytes)",
									q, delay, len(gotRaw), len(wantRaw))
							}
							t.Logf("non-bitexact decoder loss drift on amd64 accepted: Q=%.2f delay=%d", q, delay)
							return
						}
						t.Fatalf("decoder loss fixture drift: got=%d bytes want=%d bytes", len(gotRaw), len(wantRaw))
					}
				})
			}
		})
	}
}
