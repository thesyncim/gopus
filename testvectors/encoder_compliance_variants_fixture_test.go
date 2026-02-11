package testvectors

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

const encoderComplianceVariantsFixturePath = "testdata/encoder_compliance_libopus_variants_fixture.json"
const encoderVariantsBaselinePath = "testdata/encoder_compliance_variants_ratchet_baseline.json"
const updateVariantsBaselineEnv = "GOPUS_UPDATE_VARIANT_BASELINE"

type encoderComplianceVariantsFixtureFile struct {
	Version    int                                    `json:"version"`
	SampleRate int                                    `json:"sample_rate"`
	Generator  string                                 `json:"generator"`
	Variants   []string                               `json:"variants"`
	Cases      []encoderComplianceVariantsFixtureCase `json:"cases"`
}

type encoderComplianceVariantsFixtureCase struct {
	Name          string                                   `json:"name"`
	Variant       string                                   `json:"variant"`
	Mode          string                                   `json:"mode"`
	Bandwidth     string                                   `json:"bandwidth"`
	FrameSize     int                                      `json:"frame_size"`
	Channels      int                                      `json:"channels"`
	Bitrate       int                                      `json:"bitrate"`
	LibQ          float64                                  `json:"lib_q"`
	SignalFrames  int                                      `json:"signal_frames"`
	SignalSHA256  string                                   `json:"signal_sha256"`
	Frames        int                                      `json:"frames"`
	ModeHistogram map[string]int                           `json:"mode_histogram"`
	Packets       []encoderComplianceVariantsFixturePacket `json:"packets"`
}

type encoderComplianceVariantsFixturePacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

type encoderVariantsBaselineFile struct {
	Version int                         `json:"version"`
	Cases   []encoderVariantsBaselineTC `json:"cases"`
}

type encoderVariantsBaselineTC struct {
	Name             string  `json:"name"`
	Variant          string  `json:"variant"`
	MinGapDB         float64 `json:"min_gap_db"`
	MaxMeanAbsPacket float64 `json:"max_mean_abs_packet_len"`
	MaxP95AbsPacket  float64 `json:"max_p95_abs_packet_len"`
	MaxModeMismatch  float64 `json:"max_mode_mismatch_rate"`
	MaxHistogramL1   float64 `json:"max_histogram_l1"`
}

var (
	encoderComplianceVariantsFixtureOnce sync.Once
	encoderComplianceVariantsFixtureData encoderComplianceVariantsFixtureFile
	encoderComplianceVariantsFixtureErr  error
)

func loadEncoderComplianceVariantsFixture() (encoderComplianceVariantsFixtureFile, error) {
	encoderComplianceVariantsFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(encoderComplianceVariantsFixturePath))
		if err != nil {
			encoderComplianceVariantsFixtureErr = err
			return
		}
		var fixture encoderComplianceVariantsFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			encoderComplianceVariantsFixtureErr = err
			return
		}
		if len(fixture.Cases) == 0 {
			encoderComplianceVariantsFixtureErr = fmt.Errorf("encoder variants fixture has no cases")
			return
		}
		for i := range fixture.Cases {
			c := &fixture.Cases[i]
			if c.FrameSize <= 0 || c.Channels <= 0 || c.Bitrate <= 0 {
				encoderComplianceVariantsFixtureErr = fmt.Errorf("invalid metadata in variants case[%d]", i)
				return
			}
			if c.SignalFrames != 48000/c.FrameSize {
				encoderComplianceVariantsFixtureErr = fmt.Errorf("signal_frames mismatch in variants case[%d]", i)
				return
			}
			if c.Frames != len(c.Packets) {
				encoderComplianceVariantsFixtureErr = fmt.Errorf("frame count mismatch in variants case[%d]", i)
				return
			}
			if len(c.SignalSHA256) != 64 {
				encoderComplianceVariantsFixtureErr = fmt.Errorf("signal hash must be sha256 hex in variants case[%d]", i)
				return
			}
			for j := range c.Packets {
				if _, err := base64.StdEncoding.DecodeString(c.Packets[j].DataB64); err != nil {
					encoderComplianceVariantsFixtureErr = fmt.Errorf("invalid packet[%d] b64 in variants case[%d]: %w", j, i, err)
					return
				}
			}
		}
		encoderComplianceVariantsFixtureData = fixture
	})
	return encoderComplianceVariantsFixtureData, encoderComplianceVariantsFixtureErr
}

func decodeEncoderVariantsFixturePackets(c encoderComplianceVariantsFixtureCase) ([][]byte, []uint32, error) {
	packets := make([][]byte, len(c.Packets))
	ranges := make([]uint32, len(c.Packets))
	for i := range c.Packets {
		payload, err := base64.StdEncoding.DecodeString(c.Packets[i].DataB64)
		if err != nil {
			return nil, nil, err
		}
		packets[i] = payload
		ranges[i] = c.Packets[i].FinalRange
	}
	return packets, ranges, nil
}

func findEncoderVariantsFixtureCase(mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int, variant string) (encoderComplianceVariantsFixtureCase, bool) {
	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		return encoderComplianceVariantsFixtureCase{}, false
	}
	modeName := fixtureModeName(mode)
	bwName := fixtureBandwidthName(bandwidth)
	for _, c := range fixture.Cases {
		if c.Variant != variant {
			continue
		}
		if c.Mode == modeName &&
			c.Bandwidth == bwName &&
			c.FrameSize == frameSize &&
			c.Channels == channels &&
			c.Bitrate == bitrate {
			return c, true
		}
	}
	return encoderComplianceVariantsFixtureCase{}, false
}

func TestEncoderVariantsFixtureCoverage(t *testing.T) {
	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load encoder variants fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported variants fixture version: %d", fixture.Version)
	}
	if fixture.SampleRate != 48000 {
		t.Fatalf("unsupported variants fixture sample rate: %d", fixture.SampleRate)
	}

	wantVariants := testsignal.EncoderSignalVariants()
	if len(fixture.Variants) != len(wantVariants) {
		t.Fatalf("variant list mismatch: got=%d want=%d", len(fixture.Variants), len(wantVariants))
	}
	for i := range wantVariants {
		if fixture.Variants[i] != wantVariants[i] {
			t.Fatalf("variant[%d] mismatch: got=%q want=%q", i, fixture.Variants[i], wantVariants[i])
		}
	}

	seen := make(map[string]struct{}, len(fixture.Cases))
	for i, c := range fixture.Cases {
		mode, err := parseFixtureMode(c.Mode)
		if err != nil {
			t.Fatalf("case[%d] invalid mode %q: %v", i, c.Mode, err)
		}
		bw, err := parseFixtureBandwidth(c.Bandwidth)
		if err != nil {
			t.Fatalf("case[%d] invalid bandwidth %q: %v", i, c.Bandwidth, err)
		}
		key := fmt.Sprintf("%d/%d/%d/%d/%d/%s", mode, bw, c.FrameSize, c.Channels, c.Bitrate, c.Variant)
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate fixture case key %s", key)
		}
		seen[key] = struct{}{}
	}

	wantTotal := len(encoderComplianceSummaryCases()) * len(wantVariants)
	if len(fixture.Cases) != wantTotal {
		t.Fatalf("fixture case count mismatch: got=%d want=%d", len(fixture.Cases), wantTotal)
	}

	var missing []string
	for _, tc := range encoderComplianceSummaryCases() {
		for _, variant := range wantVariants {
			if _, ok := findEncoderVariantsFixtureCase(tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate, variant); !ok {
				missing = append(missing, fmt.Sprintf("%s[%s]", tc.name, variant))
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("missing variants fixture coverage: %s", strings.Join(missing, ", "))
	}
}

func TestEncoderVariantsFixtureSignalHash(t *testing.T) {
	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load encoder variants fixture: %v", err)
	}
	for _, c := range fixture.Cases {
		c := c
		name := fmt.Sprintf("%s-%s", c.Name, c.Variant)
		t.Run(name, func(t *testing.T) {
			totalSamples := c.SignalFrames * c.FrameSize * c.Channels
			signal, err := testsignal.GenerateEncoderSignalVariant(c.Variant, 48000, totalSamples, c.Channels)
			if err != nil {
				t.Fatalf("generate signal: %v", err)
			}
			hash := testsignal.HashFloat32LE(signal)
			if hash != c.SignalSHA256 {
				t.Fatalf("signal hash mismatch: got=%s want=%s", hash, c.SignalSHA256)
			}
		})
	}
}

func TestEncoderVariantsFixtureHonestyWithOpusDemo1601(t *testing.T) {
	requireTestTier(t, testTierExhaustive)

	opusDemo, ok := getFixtureOpusDemoPathForEncoder()
	if !ok {
		t.Skip("tmp_check opus_demo not found; skipping variants fixture honesty")
	}
	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load encoder variants fixture: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "gopus-enc-variants-honesty-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	for _, c := range fixture.Cases {
		c := c
		name := fmt.Sprintf("%s-%s", c.Name, c.Variant)
		t.Run(name, func(t *testing.T) {
			app, err := modeToOpusDemoApp(c.Mode)
			if err != nil {
				t.Fatalf("map mode: %v", err)
			}
			bwArg, err := bandwidthToOpusDemoArg(c.Bandwidth)
			if err != nil {
				t.Fatalf("map bandwidth: %v", err)
			}
			frameArg, err := frameSizeSamplesToArg(c.FrameSize)
			if err != nil {
				t.Fatalf("map frame size: %v", err)
			}
			totalSamples := c.SignalFrames * c.FrameSize * c.Channels
			signal, err := testsignal.GenerateEncoderSignalVariant(c.Variant, 48000, totalSamples, c.Channels)
			if err != nil {
				t.Fatalf("generate signal: %v", err)
			}
			if hash := testsignal.HashFloat32LE(signal); hash != c.SignalSHA256 {
				t.Fatalf("signal hash mismatch before opus_demo run")
			}

			rawPath := filepath.Join(tmpDir, fmt.Sprintf("%s.raw.f32", strings.ReplaceAll(name, "/", "_")))
			bitPath := filepath.Join(tmpDir, fmt.Sprintf("%s.bit", strings.ReplaceAll(name, "/", "_")))
			if err := writeFloat32LEFile(rawPath, signal); err != nil {
				t.Fatalf("write raw input: %v", err)
			}

			cmd := exec.Command(opusDemo,
				"-e", app, "48000", strconv.Itoa(c.Channels), strconv.Itoa(c.Bitrate),
				"-f32", "-cbr", "-complexity", "10", "-bandwidth", bwArg, "-framesize", frameArg,
				rawPath, bitPath,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("opus_demo encode failed: %v (%s)", err, out)
			}
			gotPackets, gotRanges, err := parseOpusDemoEncodeBitstream(bitPath)
			if err != nil {
				t.Fatalf("parse bitstream: %v", err)
			}
			wantPackets, wantRanges, err := decodeEncoderVariantsFixturePackets(c)
			if err != nil {
				t.Fatalf("decode fixture packets: %v", err)
			}
			if len(gotPackets) != len(wantPackets) {
				t.Fatalf("packet count mismatch: got=%d want=%d", len(gotPackets), len(wantPackets))
			}
			for i := range gotPackets {
				if gotRanges[i] != wantRanges[i] {
					t.Fatalf("frame %d range mismatch: got=0x%08x want=0x%08x", i, gotRanges[i], wantRanges[i])
				}
				if !bytes.Equal(gotPackets[i], wantPackets[i]) {
					t.Fatalf("frame %d payload mismatch", i)
				}
			}
		})
	}
}

type encoderVariantParityThreshold struct {
	minGapDB            float64
	maxMeanAbsPacketLen float64
	maxP95AbsPacketLen  float64
	maxModeMismatchRate float64
	maxHistogramL1      float64
}

func encoderVariantThreshold(c encoderComplianceVariantsFixtureCase) encoderVariantParityThreshold {
	out := encoderVariantParityThreshold{
		minGapDB:            -8.0,
		maxMeanAbsPacketLen: 150.0,
		maxP95AbsPacketLen:  320.0,
		maxModeMismatchRate: 1.0,
		maxHistogramL1:      2.0,
	}

	switch c.Mode {
	case "celt":
		out.maxMeanAbsPacketLen = 220.0
		out.maxP95AbsPacketLen = 450.0
		out.maxModeMismatchRate = 0.0
		out.maxHistogramL1 = 0.0
	case "silk":
		out.minGapDB = -3.0
		out.maxMeanAbsPacketLen = 90.0
		out.maxP95AbsPacketLen = 180.0
		out.maxModeMismatchRate = 0.0
		out.maxHistogramL1 = 0.0
	case "hybrid":
		out.minGapDB = -35.0
		out.maxMeanAbsPacketLen = 45.0
		out.maxP95AbsPacketLen = 110.0
		out.maxModeMismatchRate = 1.0
		out.maxHistogramL1 = 2.0
	}

	if c.Channels == 2 {
		out.maxMeanAbsPacketLen *= 1.2
		out.maxP95AbsPacketLen *= 1.2
	}
	return out
}

type encoderPacketProfileStats struct {
	meanAbsPacketLen float64
	p95AbsPacketLen  float64
	modeMismatchRate float64
	histogramL1      float64
}

func encoderVariantCaseKey(name, variant string) string {
	return name + "|" + variant
}

func loadEncoderVariantsBaseline() (map[string]encoderVariantsBaselineTC, error) {
	data, err := os.ReadFile(filepath.Join(encoderVariantsBaselinePath))
	if err != nil {
		return nil, err
	}
	var baseline encoderVariantsBaselineFile
	if err := json.Unmarshal(data, &baseline); err != nil {
		return nil, err
	}
	if baseline.Version != 1 {
		return nil, fmt.Errorf("unsupported variants baseline version: %d", baseline.Version)
	}
	out := make(map[string]encoderVariantsBaselineTC, len(baseline.Cases))
	for _, c := range baseline.Cases {
		key := encoderVariantCaseKey(c.Name, c.Variant)
		out[key] = c
	}
	return out, nil
}

func baselineMarginForMode(mode string) (gap, meanMul, p95Mul, modeAdd, histAdd float64) {
	switch mode {
	case "celt":
		return 0.75, 0.15, 0.15, 0.03, 0.03
	case "silk":
		return 0.50, 0.12, 0.12, 0.02, 0.02
	case "hybrid":
		return 2.00, 0.20, 0.20, 0.08, 0.08
	default:
		return 1.00, 0.20, 0.20, 0.05, 0.05
	}
}

func buildBaselineCase(c encoderComplianceVariantsFixtureCase, gapDB float64, stats encoderPacketProfileStats) encoderVariantsBaselineTC {
	gapMargin, meanMul, p95Mul, modeAdd, histAdd := baselineMarginForMode(c.Mode)
	maxModeMismatch := stats.modeMismatchRate + modeAdd
	if maxModeMismatch > 1.0 {
		maxModeMismatch = 1.0
	}
	maxHistogramL1 := stats.histogramL1 + histAdd
	if maxHistogramL1 > 2.0 {
		maxHistogramL1 = 2.0
	}
	return encoderVariantsBaselineTC{
		Name:             c.Name,
		Variant:          c.Variant,
		MinGapDB:         gapDB - gapMargin,
		MaxMeanAbsPacket: stats.meanAbsPacketLen * (1.0 + meanMul),
		MaxP95AbsPacket:  stats.p95AbsPacketLen * (1.0 + p95Mul),
		MaxModeMismatch:  maxModeMismatch,
		MaxHistogramL1:   maxHistogramL1,
	}
}

func packetMode(pkt []byte) string {
	if len(pkt) == 0 {
		return "empty"
	}
	cfg := int(pkt[0] >> 3)
	switch {
	case cfg <= 11:
		return "silk"
	case cfg <= 15:
		return "hybrid"
	default:
		return "celt"
	}
}

func computeEncoderPacketProfileStats(libPackets, goPackets [][]byte) encoderPacketProfileStats {
	n := len(libPackets)
	if len(goPackets) < n {
		n = len(goPackets)
	}
	if n == 0 {
		return encoderPacketProfileStats{}
	}

	diffs := make([]int, n)
	modeMismatch := 0
	libHist := map[string]int{"silk": 0, "hybrid": 0, "celt": 0}
	goHist := map[string]int{"silk": 0, "hybrid": 0, "celt": 0}
	total := 0
	for i := 0; i < n; i++ {
		diff := len(goPackets[i]) - len(libPackets[i])
		if diff < 0 {
			diff = -diff
		}
		diffs[i] = diff
		total += diff

		libMode := packetMode(libPackets[i])
		goMode := packetMode(goPackets[i])
		libHist[libMode]++
		goHist[goMode]++
		if libMode != goMode {
			modeMismatch++
		}
	}
	sort.Ints(diffs)
	p95Idx := int(math.Ceil(0.95*float64(len(diffs)))) - 1
	if p95Idx < 0 {
		p95Idx = 0
	}
	if p95Idx >= len(diffs) {
		p95Idx = len(diffs) - 1
	}

	l1 := 0
	for _, mode := range []string{"silk", "hybrid", "celt"} {
		d := goHist[mode] - libHist[mode]
		if d < 0 {
			d = -d
		}
		l1 += d
	}

	return encoderPacketProfileStats{
		meanAbsPacketLen: float64(total) / float64(n),
		p95AbsPacketLen:  float64(diffs[p95Idx]),
		modeMismatchRate: float64(modeMismatch) / float64(n),
		histogramL1:      float64(l1) / float64(n),
	}
}

func encodeGopusForVariantsCase(c encoderComplianceVariantsFixtureCase, signal []float32) ([][]byte, error) {
	mode, err := parseFixtureMode(c.Mode)
	if err != nil {
		return nil, err
	}
	bandwidth, err := parseFixtureBandwidth(c.Bandwidth)
	if err != nil {
		return nil, err
	}

	enc := encoder.NewEncoder(48000, c.Channels)
	// Fixture rows tagged as "hybrid" are generated by libopus with
	// `opus_demo -e audio`, which allows SILK/CELT mode selection to adapt.
	// Mirror that behavior with ModeAuto instead of forcing Hybrid.
	encMode := mode
	if mode == encoder.ModeHybrid {
		encMode = encoder.ModeAuto
	}
	enc.SetMode(encMode)
	enc.SetBandwidth(bandwidth)
	enc.SetBitrate(c.Bitrate)
	enc.SetBitrateMode(encoder.ModeCBR)
	// Match fixture generation (opus_demo app/profile + bitrate controls only):
	// do not force signal-type hints here.

	packets := make([][]byte, 0, c.Frames)
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
		packets = append(packets, pktCopy)
	}

	// Fixture packets may include one trailing frame from encoder buffering.
	// Flush with silence until we reach fixture frame count.
	if len(packets) < c.Frames {
		flushLimit := c.Frames + 4
		silence := make([]float64, samplesPerFrame)
		for len(packets) < c.Frames && len(packets) < flushLimit {
			pkt, err := enc.Encode(silence, c.FrameSize)
			if err != nil {
				return nil, fmt.Errorf("flush frame %d: %w", len(packets), err)
			}
			if len(pkt) == 0 {
				continue
			}
			pktCopy := make([]byte, len(pkt))
			copy(pktCopy, pkt)
			packets = append(packets, pktCopy)
		}
	}
	return packets, nil
}

// float32ToFloat64OpusDemoF32 mirrors opus_demo -f32 input conversion:
// in[i] = floor(.5 + sample*8388608) followed by opus_encode24 scaling back.
// This keeps variant-fixture parity aligned with how the libopus fixture is generated.
func float32ToFloat64OpusDemoF32(in []float32) []float64 {
	const inv24 = 1.0 / 8388608.0
	out := make([]float64, len(in))
	for i, s := range in {
		q := math.Floor(0.5 + float64(s)*8388608.0)
		out[i] = q * inv24
	}
	return out
}

func qualityFromPacketsInternal(packets [][]byte, original []float32, channels, frameSize int) (float64, error) {
	decoded, err := decodeComplianceWithInternalDecoder(packets, channels)
	if err != nil {
		return 0, err
	}
	if len(decoded) == 0 {
		return 0, fmt.Errorf("no decoded samples")
	}
	preSkip := OpusPreSkip * channels
	if len(decoded) > preSkip {
		decoded = decoded[preSkip:]
	}
	compareLen := len(original)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}
	maxDelay := 4 * frameSize
	if maxDelay < 960 {
		maxDelay = 960
	}
	q, _ := ComputeQualityFloat32WithDelay(decoded[:compareLen], original[:compareLen], 48000, maxDelay)
	return q, nil
}

func TestEncoderVariantProfileParityAgainstLibopusFixture(t *testing.T) {
	requireTestTier(t, testTierParity)

	fixture, err := loadEncoderComplianceVariantsFixture()
	if err != nil {
		t.Fatalf("load encoder variants fixture: %v", err)
	}
	updateBaseline := strings.TrimSpace(os.Getenv(updateVariantsBaselineEnv)) == "1"
	var baseline map[string]encoderVariantsBaselineTC
	if !updateBaseline {
		baseline, err = loadEncoderVariantsBaseline()
		if err != nil {
			t.Fatalf("load encoder variants baseline: %v (set %s=1 to regenerate)", err, updateVariantsBaselineEnv)
		}
	}
	generatedBaseline := make(map[string]encoderVariantsBaselineTC, len(fixture.Cases))
	seenBaseline := make(map[string]struct{}, len(fixture.Cases))

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
			goPackets, err := encodeGopusForVariantsCase(c, signal)
			if err != nil {
				t.Fatalf("encode gopus packets: %v", err)
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
			goSNR := SNRFromQuality(goQ)
			libSNR := SNRFromQuality(libQ)
			gapDB := goSNR - libSNR

			thr := encoderVariantThreshold(c)
			t.Logf("gap=%.2fdB meanAbs=%.2f p95Abs=%.2f mismatch=%.2f%% histL1=%.3f",
				gapDB,
				stats.meanAbsPacketLen,
				stats.p95AbsPacketLen,
				100*stats.modeMismatchRate,
				stats.histogramL1,
			)

			if gapDB < thr.minGapDB {
				t.Fatalf("quality gap regression: gap=%.2f dB < floor %.2f dB", gapDB, thr.minGapDB)
			}
			if stats.meanAbsPacketLen > thr.maxMeanAbsPacketLen {
				t.Fatalf("mean abs packet length diff regression: %.2f > %.2f", stats.meanAbsPacketLen, thr.maxMeanAbsPacketLen)
			}
			if stats.p95AbsPacketLen > thr.maxP95AbsPacketLen {
				t.Fatalf("p95 abs packet length diff regression: %.2f > %.2f", stats.p95AbsPacketLen, thr.maxP95AbsPacketLen)
			}
			if stats.modeMismatchRate > thr.maxModeMismatchRate {
				t.Fatalf("mode mismatch regression: %.4f > %.4f", stats.modeMismatchRate, thr.maxModeMismatchRate)
			}
			if stats.histogramL1 > thr.maxHistogramL1 {
				t.Fatalf("mode histogram L1 regression: %.3f > %.3f", stats.histogramL1, thr.maxHistogramL1)
			}

			key := encoderVariantCaseKey(c.Name, c.Variant)
			seenBaseline[key] = struct{}{}
			if updateBaseline {
				generatedBaseline[key] = buildBaselineCase(c, gapDB, stats)
				return
			}
			b, ok := baseline[key]
			if !ok {
				t.Fatalf("missing ratchet baseline for %s", key)
			}
			if gapDB < b.MinGapDB {
				t.Fatalf("ratchet gap regression: %.2f < %.2f", gapDB, b.MinGapDB)
			}
			if stats.meanAbsPacketLen > b.MaxMeanAbsPacket {
				t.Fatalf("ratchet meanAbs regression: %.2f > %.2f", stats.meanAbsPacketLen, b.MaxMeanAbsPacket)
			}
			if stats.p95AbsPacketLen > b.MaxP95AbsPacket {
				t.Fatalf("ratchet p95 regression: %.2f > %.2f", stats.p95AbsPacketLen, b.MaxP95AbsPacket)
			}
			if stats.modeMismatchRate > b.MaxModeMismatch {
				t.Fatalf("ratchet mode mismatch regression: %.4f > %.4f", stats.modeMismatchRate, b.MaxModeMismatch)
			}
			if stats.histogramL1 > b.MaxHistogramL1 {
				t.Fatalf("ratchet histogram L1 regression: %.3f > %.3f", stats.histogramL1, b.MaxHistogramL1)
			}
		})
	}

	if updateBaseline {
		keys := make([]string, 0, len(generatedBaseline))
		for k := range generatedBaseline {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := encoderVariantsBaselineFile{
			Version: 1,
			Cases:   make([]encoderVariantsBaselineTC, 0, len(keys)),
		}
		for _, k := range keys {
			out.Cases = append(out.Cases, generatedBaseline[k])
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			t.Fatalf("marshal generated baseline: %v", err)
		}
		if err := os.WriteFile(filepath.Join(encoderVariantsBaselinePath), append(data, '\n'), 0o644); err != nil {
			t.Fatalf("write generated baseline: %v", err)
		}
		t.Fatalf("updated baseline at %s; rerun without %s", encoderVariantsBaselinePath, updateVariantsBaselineEnv)
	}

	if len(baseline) != len(seenBaseline) {
		t.Fatalf("ratchet baseline coverage mismatch: baseline=%d fixtureCases=%d", len(baseline), len(seenBaseline))
	}
	for key := range baseline {
		if _, ok := seenBaseline[key]; !ok {
			t.Fatalf("ratchet baseline has stale case: %s", key)
		}
	}
}
