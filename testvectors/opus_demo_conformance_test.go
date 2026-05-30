// Package testvectors: opus_demo end-to-end conformance harness.
//
// This file drives the canonical libopus 1.6.1 `opus_demo` CLI (built by
// tools/ensure_libopus.sh) and the gopus encoder/decoder over identical PCM
// inputs across a (channels × bandwidth × frame size × bitrate × application ×
// FEC × DTX) matrix, then asserts:
//
//   - DECODE: sample-exact parity between the gopus decode of the opus_demo
//     reference bitstream and the opus_demo `-d` decode of the same bitstream,
//     for the cells whose decode path is bit-exact (SILK on all platforms;
//     CELT/hybrid on amd64), and a high quality floor for the arm64 CELT/hybrid
//     ≤1-ULP residual. This is the harness's strongest, sample-level gate.
//   - ENCODE: a decoded-quality floor between the gopus-encoded and
//     opus_demo-encoded streams (both decoded by opus_demo). Byte-exact encode
//     parity for specific forced-mode configurations is gated separately by the
//     dedicated-oracle tests (encoder_cbr_byte_parity_test.go and friends);
//     opus_demo's auto-mode encoder makes per-frame mode/redundancy decisions
//     that gopus's auto-mode encoder is not required to reproduce byte-for-byte.
//
// Wire-format / reference notes:
//   - opus_demo encode (`-e app rate ch bitrate -f32 ... in out`) reads raw
//     float32 LE PCM and writes a length-prefixed bitstream: each packet is a
//     big-endian u32 length, a big-endian u32 encoder final range, then the
//     packet bytes (src/opus_demo.c int_to_char / char_to_int).
//   - opus_demo with `-f32` quantizes each float sample to a 24-bit integer via
//     floor(.5 + s*8388608) and calls opus_encode24(); in the float build
//     opus_res is float so the encoder input is exactly q/8388608.  The harness
//     feeds the *same* quantized float values to the gopus float Encode() path
//     so both encoders observe identical input (matchOpusDemoF32Input).
//   - Byte-exactness scope follows the documented per-arch budget: SILK is pure
//     fixed-point and byte-exact on all platforms; CELT/Hybrid are byte-exact on
//     amd64 (integer CELT path) but carry a ≤1-ULP FMA residual on darwin/arm64
//     (project_arm64_celt_1ulp_drift.md), reported as an honest diff count.
//
// Gating: runs only at the parity tier with GOPUS_STRICT_LIBOPUS_REF=1 (the
// CI conformance lane sets both), so it is skipped in the fast package sweep.
package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

// conformanceSampleRate is the working rate for the harness. 48 kHz lets the
// opus_demo `-bandwidth` flag reach every NB..FB cell from a single input.
const conformanceSampleRate = 48000

// conformanceApp identifies an opus_demo application string and its gopus
// encoder configuration.
type conformanceApp struct {
	name      string // opus_demo application argument
	configure func(*encoder.Encoder)
}

var conformanceApps = map[string]conformanceApp{
	"voip": {
		name:      "voip",
		configure: func(e *encoder.Encoder) { e.SetVoIPApplication(true) },
	},
	"audio": {
		name:      "audio",
		configure: func(e *encoder.Encoder) {},
	},
	"restricted-lowdelay": {
		name:      "restricted-lowdelay",
		configure: func(e *encoder.Encoder) { e.SetLowDelay(true) },
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

// decodeSampleExactExpected reports whether decoding the given reference
// packets with the gopus decoder is expected to be sample-identical to the
// opus_demo decode on the current architecture.
//
// The decision is driven by the *actual* per-packet mode in the reference
// bitstream rather than the requested bandwidth, because opus_demo's auto-mode
// encoder may emit hybrid (SILK+CELT) packets for e.g. WB/VoIP. The SILK decode
// path is pure fixed point and sample-exact on every platform; the CELT and
// hybrid decode paths are sample-exact on amd64 (integer + non-FMA float) but
// carry a ≤1-ULP FMA residual on darwin/arm64 (project_arm64_celt_1ulp_drift.md),
// so a bitstream containing any CELT/hybrid packet falls back to a very high
// quality floor on arm64.
func decodeSampleExactExpected(packets [][]byte) bool {
	if runtime.GOARCH == "amd64" {
		return true
	}
	for _, pkt := range packets {
		info, err := gopus.ParsePacket(pkt)
		if err != nil {
			return false
		}
		if info.TOC.Mode != gopus.ModeSILK {
			return false // CELT/hybrid packet engages the arm64 FMA path
		}
	}
	return true
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

		// --- CELT / hybrid audio cells: amd64 byte-exact, arm64 residual ---
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
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return out
}

// configureGopusEncoder builds a gopus encoder matching the opus_demo cell.
func configureGopusEncoder(c conformanceCell) *encoder.Encoder {
	enc := encoder.NewEncoder(conformanceSampleRate, c.channels)
	conformanceApps[c.app].configure(enc)
	enc.SetBandwidth(conformanceBandwidth[c.bandwidth])
	enc.SetBitrate(c.bitrate)
	enc.SetComplexity(10)
	enc.SetVBR(!c.cbr)
	enc.SetFEC(c.fec)
	enc.SetDTX(c.dtx)
	return enc
}

// gopusEncodePackets encodes the PCM frame-by-frame, mirroring the opus_demo
// per-frame encode loop.
func gopusEncodePackets(t *testing.T, c conformanceCell, pcm []float32) [][]byte {
	t.Helper()
	enc := configureGopusEncoder(c)
	frameSize := conformanceFrameMs[c.frame]
	step := frameSize * c.channels
	var packets [][]byte
	for off := 0; off+step <= len(pcm); off += step {
		pkt, err := enc.Encode(pcm[off:off+step], frameSize)
		if err != nil {
			t.Fatalf("gopus Encode: %v", err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// gopusDecodePackets decodes a packet list with the gopus root decoder.
func gopusDecodePackets(t *testing.T, channels int, packets [][]byte) []float32 {
	t.Helper()
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(conformanceSampleRate, channels))
	if err != nil {
		t.Fatalf("gopus NewDecoder: %v", err)
	}
	buf := make([]float32, 5760*channels)
	var out []float32
	for _, pkt := range packets {
		n, err := dec.Decode(pkt, buf)
		if err != nil {
			t.Fatalf("gopus Decode: %v", err)
		}
		out = append(out, buf[:n*channels]...)
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// TestOpusDemoEndToEndConformance is the opus_demo-driven encode/decode
// conformance harness. For each matrix cell it:
//
//  1. generates a deterministic testsignal,
//  2. quantizes it to opus_demo's `-f32` 24-bit input representation,
//  3. encodes the input with opus_demo and decodes that reference bitstream with
//     both opus_demo and gopus, gating the gopus decode sample-exact where the
//     decode path is bit-exact (SILK everywhere; CELT/hybrid on amd64) and to a
//     high quality floor on the arm64 CELT/hybrid ≤1-ULP residual,
//  4. encodes the same input with gopus, decodes both the gopus and opus_demo
//     encoder outputs with opus_demo, and holds the gopus-encoded stream to a
//     decoded-quality floor (the two encoders' auto-mode per-frame decisions are
//     not required to be byte-identical).
func TestOpusDemoEndToEndConformance(t *testing.T) {
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)

	opusDemo, err := benchutil.OpusDemoPath()
	if err != nil {
		t.Skipf("opus_demo unavailable: %v", err)
	}

	// Two seconds of signal at 48 kHz is enough to exercise multiple frames of
	// every frame size while staying CI-fast.
	const totalSamples = conformanceSampleRate * 2

	for _, c := range conformanceMatrix() {
		c := c
		t.Run(c.label(), func(t *testing.T) {
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

			// --- DECODE conformance: gopus decodes the opus_demo reference
			// bitstream and is compared against opus_demo's own decode of the
			// same bitstream. This isolates the decoder paths from any
			// encoder-side mode-decision differences. A pure-SILK bitstream is
			// gated sample-exact on all platforms; any CELT/hybrid packet is
			// gated sample-exact on amd64 and held to a high quality floor on
			// arm64 (driven by the actual per-packet mode, not the requested
			// bandwidth).
			refDecPCM := runOpusDemoDecode(t, opusDemo, c.channels, refPackets)
			gotDecPCM := gopusDecodePackets(t, c.channels, refPackets)
			assertDecodeParity(t, c, refPackets, gotDecPCM, refDecPCM)

			// --- ENCODE conformance: gopus's auto-mode encoder makes per-frame
			// mode/redundancy decisions that need not be byte-identical to
			// opus_demo's auto-mode encoder, so we hold the gopus-encoded stream
			// to a quality floor against the original input PCM, scored through
			// the same canonical opus_compare comparator (decoded-vs-original)
			// the encoder-compliance suite uses. The opus_demo-encoded stream is
			// scored the same way as a reference for context. We also sanity-
			// check that gopus produces a comparable packet count.
			if d := abs(len(gopusPackets) - len(refPackets)); d > 1 {
				t.Errorf("ENCODE packet-count mismatch: gopus=%d opus_demo=%d", len(gopusPackets), len(refPackets))
			}
			assertEncodeQuality(t, c, gopusPackets, refPackets, pcm)
		})
	}
}

// assertDecodeParity gates the gopus decode of the opus_demo reference
// bitstream against the opus_demo decode: sample-exact where expected, and a
// high quality floor otherwise.
func assertDecodeParity(t *testing.T, c conformanceCell, refPackets [][]byte, got, ref []float32) {
	t.Helper()
	n := len(ref)
	if len(got) < n {
		n = len(got)
	}
	if n == 0 {
		t.Fatal("decode produced no samples")
	}
	got, ref = got[:n], ref[:n]

	if decodeSampleExactExpected(refPackets) {
		if bytes.Equal(float32sToBytes(ref), float32sToBytes(got)) {
			return // strongest assertion: bit-identical decode
		}
		t.Errorf("DECODE sample parity FAILED (expected sample-exact on %s)", runtime.GOARCH)
		return
	}
	// arm64 CELT/hybrid: ≤1-ULP FMA residual; hold a very high quality floor.
	const arm64DecodeQualityFloor = 99.0
	q, _, err := ComputeOpusCompareQualityFloat32WithDelay(got, ref, conformanceSampleRate, c.channels, 960)
	if err != nil {
		t.Fatalf("opus_compare quality: %v", err)
	}
	if q < arm64DecodeQualityFloor {
		t.Errorf("DECODE quality below arm64 floor: Q=%.3f (< %.2f)", q, arm64DecodeQualityFloor)
	} else {
		t.Logf("DECODE arm64 residual OK: Q=%.3f", q)
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
