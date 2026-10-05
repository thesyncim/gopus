// Package testvectors: selected-libopus CLI conformance.
//
// The harness drives opus_demo and gopus over identical, quantized PCM across
// channels, bandwidths, durations, bitrates, applications, FEC, and DTX.
// Decode compares exact samples through equivalent public output formats.
// Encoder quality is measured separately; dedicated encoder oracles gate packet
// bytes and final ranges with matching controls and input framing.
//
// opus_demo -f32 reads float32 LE PCM, quantizes it to 24-bit samples, and calls
// opus_encode24. Its decode path calls opus_decode24 and writes each result as
// float32(value)/8388608 (src/opus_demo.c FORMAT_F32_LE branches). Comparing that
// output with opus_decode_float is not a same-format exactness check. This test
// compares Go DecodeInt24 with the CLI output and independently compares Go
// Decode with the selected C opus_decode_float helper.
//
// The CLI bitstream uses big-endian u32 packet lengths and encoder final ranges,
// followed by packet bytes. The tests require the parity tier and strict live C.
package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

// conformanceSampleRate is the working rate for the harness. 48 kHz lets the
// opus_demo `-bandwidth` flag reach every NB..FB cell from a single input.
const conformanceSampleRate = 48000

// conformanceApp identifies an opus_demo application string and its gopus
// encoder configuration.
type conformanceApp struct {
	name        string // opus_demo application argument
	application gopus.Application
}

var conformanceApps = map[string]conformanceApp{
	"voip": {
		name: "voip", application: gopus.ApplicationVoIP,
	},
	"audio": {
		name: "audio", application: gopus.ApplicationAudio,
	},
	"restricted-lowdelay": {
		name: "restricted-lowdelay", application: gopus.ApplicationLowDelay,
	},
}

// conformanceBandwidth maps the opus_demo bandwidth token to the gopus type.
var conformanceBandwidth = map[string]types.Bandwidth{
	"NB":  types.BandwidthNarrowband,
	"MB":  types.BandwidthMediumband,
	"WB":  types.BandwidthWideband,
	"SWB": types.BandwidthSuperwideband,
	"FB":  types.BandwidthFullband,
}

// conformanceFrameMs maps the opus_demo frame-size token to samples at 48 kHz.
var conformanceFrameMs = map[string]int{
	"10": 480,
	"20": 960,
	"40": 1920,
	"60": 2880,
}

// conformanceCell is one row of the conformance matrix.
type conformanceCell struct {
	channels  int
	app       string // key into conformanceApps
	bandwidth string // key into conformanceBandwidth
	frame     string // key into conformanceFrameMs
	bitrate   int
	cbr       bool
	fec       bool
	dtx       bool
	signal    string // testsignal corpus class
}

func (c conformanceCell) label() string {
	return fmt.Sprintf("%dch-%s-%s-%sms-%dk-%s-fec%v-dtx%v-%s",
		c.channels, c.app, c.bandwidth, c.frame, c.bitrate/1000,
		map[bool]string{true: "cbr", false: "vbr"}[c.cbr], c.fec, c.dtx, c.signal)
}

// conformanceMatrix returns the CI-fast conformance matrix. It is intentionally
// curated (not a full cross-product) to stay deterministic and fast while
// covering every axis the task enumerates.
func conformanceMatrix() []conformanceCell {
	speech := testsignal.CorpusCleanSpeechV1
	music := testsignal.CorpusMusicV1
	transient := testsignal.CorpusCastanetTransientV1
	stereo := testsignal.CorpusStereoDecorrelatedV1
	speechNoise := testsignal.CorpusSpeechInNoiseV1
	silence := testsignal.CorpusSilenceBurstsV1
	return []conformanceCell{
		// --- SILK voip cells: byte-exact on all platforms ---
		{1, "voip", "NB", "20", 16000, true, false, false, speech},
		{1, "voip", "MB", "20", 20000, true, false, false, speech},
		{1, "voip", "WB", "10", 24000, true, false, false, speech},
		{1, "voip", "WB", "20", 24000, false, false, false, speech},
		{1, "voip", "WB", "40", 24000, true, false, false, speech},
		{1, "voip", "WB", "60", 24000, true, false, false, speech},
		{2, "voip", "WB", "20", 32000, true, false, false, stereo},
		// SILK with FEC / DTX axes
		{1, "voip", "WB", "20", 24000, false, true, false, speechNoise},
		{1, "voip", "WB", "20", 16000, false, false, true, silence},

		// --- CELT / Hybrid audio cells ---
		{1, "audio", "SWB", "20", 64000, true, false, false, music},
		{1, "audio", "FB", "20", 96000, true, false, false, music},
		{1, "audio", "FB", "10", 128000, true, false, false, transient},
		{2, "audio", "FB", "20", 128000, true, false, false, music},
		{2, "audio", "FB", "20", 96000, false, false, false, music},
		{1, "audio", "FB", "40", 64000, true, false, false, music},

		// --- restricted-lowdelay (CELT-only) cells ---
		{1, "restricted-lowdelay", "FB", "20", 96000, true, false, false, music},
		{2, "restricted-lowdelay", "FB", "10", 128000, true, false, false, music},
	}
}

// matchOpusDemoF32Input quantizes float PCM to opus_demo's `-f32` 24-bit
// integer representation and back, so the gopus float encoder observes exactly
// the same per-sample input as opus_encode24() inside opus_demo.
//
// Reference: src/opus_demo.c FORMAT_F32_LE branch:
//
//	in[i] = (int)floor(.5 + s.f*8388608);  // 24-bit integer
//
// and opus_encoder.c opus_encode24(): in[i] = INT24TORES(pcm[i]) = pcm/8388608.
func matchOpusDemoF32Input(pcm []float32) []float32 {
	out := make([]float32, len(pcm))
	for i, s := range pcm {
		q := math.Floor(0.5 + float64(s)*8388608.0)
		out[i] = float32(q / 8388608.0)
	}
	return out
}

// opusDemoBitstreamPackets parses an opus_demo length-prefixed bitstream into
// individual packets (big-endian u32 length, u32 final range, payload).
func opusDemoBitstreamPackets(data []byte) ([][]byte, []uint32, error) {
	var packets [][]byte
	var ranges []uint32
	for off := 0; off < len(data); {
		if off+8 > len(data) {
			return nil, nil, fmt.Errorf("truncated header at offset %d", off)
		}
		n := int(binary.BigEndian.Uint32(data[off : off+4]))
		r := binary.BigEndian.Uint32(data[off+4 : off+8])
		off += 8
		if n < 0 || off+n > len(data) {
			return nil, nil, fmt.Errorf("packet length %d overruns buffer at offset %d", n, off)
		}
		packets = append(packets, append([]byte(nil), data[off:off+n]...))
		ranges = append(ranges, r)
		off += n
	}
	return packets, ranges, nil
}

// runOpusDemoEncode drives `opus_demo -e ...` over the given float PCM and
// returns the produced packets (one per frame).
func runOpusDemoEncode(t *testing.T, opusDemo string, c conformanceCell, pcm []float32) [][]byte {
	t.Helper()
	dir := t.TempDir()
	inPath := filepath.Join(dir, "in.f32")
	bitPath := filepath.Join(dir, "out.bit")
	if err := benchutil.WriteRepeatedRawFloat32(inPath, pcm, 1); err != nil {
		t.Fatalf("write input pcm: %v", err)
	}
	args := []string{
		"-e", conformanceApps[c.app].name, fmt.Sprint(conformanceSampleRate),
		fmt.Sprint(c.channels), fmt.Sprint(c.bitrate),
		"-f32", "-complexity", "10",
		"-bandwidth", c.bandwidth, "-framesize", c.frame,
	}
	if c.cbr {
		args = append(args, "-cbr")
	}
	if c.fec {
		args = append(args, "-inbandfec")
	}
	if c.dtx {
		args = append(args, "-dtx")
	}
	args = append(args, inPath, bitPath)

	cmd := exec.Command(opusDemo, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("opus_demo encode failed: %v\nargs=%v\n%s", err, args, bytes.TrimSpace(out))
	}
	data, err := os.ReadFile(bitPath)
	if err != nil {
		t.Fatalf("read bitstream: %v", err)
	}
	packets, _, err := opusDemoBitstreamPackets(data)
	if err != nil {
		t.Fatalf("parse bitstream: %v", err)
	}
	return packets
}

// runOpusDemoDecode drives `opus_demo -d rate ch in.bit out.f32` and returns the
// decoded float32 PCM (interleaved).
func runOpusDemoDecode(t *testing.T, opusDemo string, channels int, packets [][]byte) []float32 {
	t.Helper()
	dir := t.TempDir()
	bitPath := filepath.Join(dir, "dec.bit")
	outPath := filepath.Join(dir, "dec.f32")
	if err := benchutil.WriteRepeatedOpusDemoBitstream(bitPath, packets, 1); err != nil {
		t.Fatalf("write bitstream: %v", err)
	}
	args := []string{"-d", fmt.Sprint(conformanceSampleRate), fmt.Sprint(channels), "-f32", bitPath, outPath}
	cmd := exec.Command(opusDemo, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("opus_demo decode failed: %v\nargs=%v\n%s", err, args, bytes.TrimSpace(out))
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read decoded pcm: %v", err)
	}
	n := len(raw) / 4
	out := make([]float32, n)
	for i := range n {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return out
}

// gopusEncodePackets uses the same public int24 input boundary and controls as
// opus_demo -f32. The PCM is already quantized by matchOpusDemoF32Input.
func gopusEncodePackets(t *testing.T, c conformanceCell, pcm []float32) [][]byte {
	t.Helper()
	cfg := gopus.EncoderConfig{
		SampleRate:  conformanceSampleRate,
		Channels:    c.channels,
		Application: conformanceApps[c.app].application,
	}
	enc, err := gopus.NewEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	frameSize := conformanceFrameMs[c.frame]
	for _, set := range []func() error{
		func() error { return enc.SetBandwidth(conformanceBandwidth[c.bandwidth]) },
		func() error { return enc.SetBitrate(c.bitrate) },
		func() error { return enc.SetComplexity(10) },
		func() error { return enc.SetLSBDepth(24) },
		func() error { return enc.SetFrameSize(frameSize) },
		func() error { return enc.SetPacketLoss(0) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	enc.SetVBR(!c.cbr)
	enc.SetVBRConstraint(false)
	enc.SetFEC(c.fec)
	enc.SetDTX(c.dtx)
	step := frameSize * c.channels
	input := make([]int32, step)
	packet := make([]byte, 15000) // src/opus_demo.c: MAX_PACKET
	var packets [][]byte
	for off := 0; off+step <= len(pcm); off += step {
		for i := range input {
			input[i] = int32(pcm[off+i] * 8388608)
		}
		n, err := enc.EncodeInt24(input, packet)
		if err != nil {
			t.Fatalf("gopus EncodeInt24: %v", err)
		}
		packets = append(packets, append([]byte(nil), packet[:n]...))
	}
	return packets
}

// gopusDecodePackets compares both public output formats with their matching
// C APIs on the same packets, then returns the CLI-compatible int24 samples.
func gopusDecodePackets(t *testing.T, channels int, packets [][]byte) []float32 {
	t.Helper()
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(conformanceSampleRate, channels))
	if err != nil {
		t.Fatalf("gopus NewDecoder: %v", err)
	}
	dec24, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(conformanceSampleRate, channels))
	if err != nil {
		t.Fatalf("gopus NewDecoder int24: %v", err)
	}
	const capacity = 5760
	buf := make([]float32, capacity*channels)
	buf24 := make([]int32, capacity*channels)
	var out, floatOutput []float32
	for i, pkt := range packets {
		n, err := dec.Decode(pkt, buf)
		if err != nil {
			t.Fatalf("gopus Decode packet %d: %v", i, err)
		}
		n24, err := dec24.DecodeInt24(pkt, buf24)
		if err != nil {
			t.Fatalf("gopus DecodeInt24 packet %d: %v", i, err)
		}
		if n != n24 {
			t.Fatalf("packet %d samples: float=%d int24=%d", i, n, n24)
		}
		floatOutput = append(floatOutput, buf[:n*channels]...)
		for _, sample := range buf24[:n24*channels] {
			out = append(out, float32(sample)*(1.0/8388608.0))
		}
	}
	wantFloat, err := decodeWithLibopusReferencePacketsSingle(channels, capacity, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "conformance float decode", err)
	}
	assertConformanceSampleBits(t, "opus_decode_float", floatOutput, wantFloat)
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// TestOpusDemoEndToEndConformance compares decoded samples through matching
// C APIs and measures encoder quality using the same canonical decoder.
func TestOpusDemoEndToEndConformance(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)

	opusDemo, err := libopustest.PublicAPIOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "opus_demo", err)
		return
	}

	// Two seconds of signal at 48 kHz is enough to exercise multiple frames of
	// every frame size while staying CI-fast.
	const totalSamples = conformanceSampleRate * 2

	for _, c := range conformanceMatrix() {
		t.Run(c.label(), func(t *testing.T) {
			t.Parallel()

			raw, err := testsignal.GenerateCorpusSignal(c.signal, conformanceSampleRate, totalSamples, c.channels)
			if err != nil {
				t.Fatalf("generate signal %q: %v", c.signal, err)
			}
			pcm := matchOpusDemoF32Input(raw)

			refPackets := runOpusDemoEncode(t, opusDemo, c, pcm)
			if len(refPackets) == 0 {
				t.Fatal("opus_demo produced no packets")
			}
			gopusPackets := gopusEncodePackets(t, c, pcm)
			if len(gopusPackets) == 0 {
				t.Fatal("gopus produced no packets")
			}

			// Both CLI outputs use opus_decode24 semantics. The Go helper also
			// checks Decode against the independent C opus_decode_float API.
			refDecPCM := runOpusDemoDecode(t, opusDemo, c.channels, refPackets)
			gotDecPCM := gopusDecodePackets(t, c.channels, refPackets)
			assertDecodeParity(t, c, gotDecPCM, refDecPCM)

			// Score both encoders against the original input through the same
			// decoder and comparator. opus_demo can emit a final padded frame.
			if d := abs(len(gopusPackets) - len(refPackets)); d > 1 {
				t.Errorf("ENCODE packet-count mismatch: gopus=%d opus_demo=%d", len(gopusPackets), len(refPackets))
			}
			assertEncodeQuality(t, c, gopusPackets, refPackets, pcm)
		})
	}
}

// assertDecodeParity requires CLI-compatible sample equality and retains the
// independent decoded-quality check on the identical, sample-aligned outputs.
func assertDecodeParity(t *testing.T, c conformanceCell, got, ref []float32) {
	t.Helper()
	assertConformanceSampleBits(t, "opus_decode24", got, ref)
	const decodeQualityFloor = 99.0
	q, _, err := ComputeOpusCompareQualityFloat32WithDelay(got, ref, conformanceSampleRate, c.channels, 960)
	if err != nil {
		t.Fatalf("opus_compare quality: %v", err)
	}
	if q < decodeQualityFloor {
		t.Errorf("DECODE quality below floor: Q=%.3f (< %.2f)", q, decodeQualityFloor)
	}
}

func assertConformanceSampleBits(t *testing.T, format string, got, want []float32) {
	t.Helper()
	if len(got) == 0 || len(got) != len(want) {
		t.Fatalf("%s sample count: Go=%d C=%d", format, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s sample %d: Go=%08x C=%08x", format, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

// Encode-quality gap tolerances. The gopus and opus_demo packet streams are both
// decoded by the libopus reference decoder and delay-searched against the
// original input through the identical comparator, so the absolute Q values
// (which can be large-magnitude artifacts of opus_compare's perceptual alignment
// on a given signal) largely cancel and only the gopus-vs-libopus gap is gated.
//
// In the well-aligned region (|refQ| small) a tight absolute tolerance applies;
// where opus_compare alignment yields large-magnitude scores (an unreliable
// region for some speech-like signals) the tolerance widens proportionally to
// the reference magnitude so alignment noise is not mistaken for a regression.
const (
	encodeQualityGapAbsToleranceQ = 1.5  // absolute Q slack in the reliable region
	encodeQualityGapRelTolerance  = 0.15 // fraction of |refQ| added to the slack
)

// encodeQualityGapFloor returns the most negative gap (gopusQ − refQ) tolerated
// for a reference quality of refQ.
func encodeQualityGapFloor(refQ float64) float64 {
	slack := encodeQualityGapAbsToleranceQ
	if rel := math.Abs(refQ) * encodeQualityGapRelTolerance; rel > slack {
		slack = rel
	}
	return -slack
}

// assertEncodeQuality gates the gopus-encoded packet stream against the
// opus_demo-encoded stream on decoded quality relative to the original input
// PCM, scored through the canonical opus_compare comparator (qualityOfPackets
// decodes with the libopus reference decoder and delay-searches against the
// original). The two encoders' auto-mode decisions may differ, so this gates the
// gopus-vs-libopus quality gap rather than asserting a byte/sample match.
func assertEncodeQuality(t *testing.T, c conformanceCell, gopusPackets, refPackets [][]byte, original []float32) {
	t.Helper()
	frameSize := conformanceFrameMs[c.frame]

	gopusCmp, _, err := qualityOfPackets(gopusPackets, original, c.channels, frameSize)
	if err != nil {
		t.Fatalf("score gopus packets: %v", err)
	}
	refCmp, _, err := qualityOfPackets(refPackets, original, c.channels, frameSize)
	if err != nil {
		t.Fatalf("score opus_demo packets: %v", err)
	}

	gap := gopusCmp.Q - refCmp.Q
	floor := encodeQualityGapFloor(refCmp.Q)
	if gap < floor {
		t.Errorf("ENCODE quality gap below floor: gopus Q=%.2f opus_demo Q=%.2f gap=%.2f (< %.2f)",
			gopusCmp.Q, refCmp.Q, gap, floor)
	} else {
		t.Logf("ENCODE quality OK: gopus Q=%.2f opus_demo Q=%.2f gap=%.2f (floor=%.2f)",
			gopusCmp.Q, refCmp.Q, gap, floor)
	}
}

// float32sToBytes reinterprets a []float32 as little-endian bytes for exact
// comparison.
func float32sToBytes(s []float32) []byte {
	b := make([]byte, len(s)*4)
	for i, v := range s {
		binary.LittleEndian.PutUint32(b[i*4:i*4+4], math.Float32bits(v))
	}
	return b
}
