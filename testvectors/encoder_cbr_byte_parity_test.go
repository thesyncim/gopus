// Package testvectors provides CBR byte-exact encoder parity tests.
//
// This file drives the gopus Encoder with SetVBR(false) (CBR) across the
// compliance summary matrix (SILK / CELT / Hybrid × representative
// rates/frames/channels) and asserts byte-identical packets against the
// pinned libopus 1.6.1 C encoder oracle built from
// tools/csrc/libopus_cbr_encode_packets.c.
//
// Conformance scope:
//   - SILK (all cells): hard byte-equality gate — libopus SILK CBR is
//     deterministic from pure Go integer/fixed-point arithmetic.
//   - CELT / Hybrid: hard gate on amd64 (integer CELT path is bit-exact on
//     that arch); on darwin/arm64 the CELT sub-band uses FMA-contracted float
//     arithmetic that diverges from clang's -ffp-contract=on by ≤1 ULP per
//     operation (see project_arm64_celt_1ulp_drift.md).  Arm64 cells report
//     the exact byte-diff count as an honest residual rather than masking.
//
// Reference:
//   - libopus src/opus_demo.c: -cbr flag → opus_encoder_ctl(enc, OPUS_SET_VBR(0))
//   - libopus src/opus_encoder.c: opus_encode_float()
//   - tools/csrc/libopus_cbr_encode_packets.c: oracle wire format "GCBR"/"GCBO"
package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

// cbrEncoderOracle application codes (map to libopus OPUS_APPLICATION_* constants)
const (
	cbrOracleAppAudio          = uint32(0) // OPUS_APPLICATION_AUDIO
	cbrOracleAppVoIP           = uint32(1) // OPUS_APPLICATION_VOIP
	cbrOracleAppRestrictedSilk = uint32(2) // OPUS_APPLICATION_RESTRICTED_SILK
	cbrOracleAppRestrictedCELT = uint32(3) // OPUS_APPLICATION_RESTRICTED_CELT
)

// cbrEncoderOracle bandwidth codes (match libopus OPUS_BANDWIDTH_* values)
const (
	cbrOracleBWNarrowband    = uint32(1101)
	cbrOracleBWMediumband    = uint32(1102)
	cbrOracleBWWideband      = uint32(1103)
	cbrOracleBWSuperWideband = uint32(1104)
	cbrOracleBWFullband      = uint32(1105)
)

var cbrOracleHelperCache libopustest.HelperCache

func cbrEncoderOraclePath() (string, error) {
	return cbrOracleHelperCache.CHelperPath(libopustest.CHelperConfig{
		Label:      "CBR encode",
		OutputBase: "gopus_libopus_cbr_encode_packets",
		SourceFile: "libopus_cbr_encode_packets.c",
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
}

// cbrOracleInput encodes the input payload for the libopus CBR oracle.
// Wire format: "GCBR" u32(1) u32(app) u32(bw) u32(ch) u32(bitrate)
//
//	u32(frame_size) u32(complexity) u32(num_frames) [pcm float32 LE …]
func cbrOracleInput(appCode, bwCode, channels, bitrate, frameSize, complexity, numFrames uint32, pcm []float32) []byte {
	size := 4 + 8*4 + len(pcm)*4
	buf := make([]byte, 0, size)
	buf = append(buf, "GCBR"...)
	var tmp [4]byte
	pu32 := func(v uint32) {
		binary.LittleEndian.PutUint32(tmp[:], v)
		buf = append(buf, tmp[:]...)
	}
	pu32(1)
	pu32(appCode)
	pu32(bwCode)
	pu32(channels)
	pu32(bitrate)
	pu32(frameSize)
	pu32(complexity)
	pu32(numFrames)
	for _, s := range pcm {
		binary.LittleEndian.PutUint32(tmp[:], math.Float32bits(s))
		buf = append(buf, tmp[:]...)
	}
	return buf
}

// parseCBROracleOutput parses the libopus CBR oracle output.
// Wire format: "GCBO" u32(1) u32(num_packets) [u32(len) bytes …]
func parseCBROracleOutput(data []byte) ([][]byte, error) {
	if len(data) < 12 || string(data[:4]) != "GCBO" {
		preview := 4
		if len(data) < preview {
			preview = len(data)
		}
		return nil, fmt.Errorf("bad CBR oracle output magic (got %q)", data[:preview])
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != 1 {
		return nil, fmt.Errorf("CBR oracle output version=%d want 1", version)
	}
	numPackets := int(binary.LittleEndian.Uint32(data[8:12]))
	packets := make([][]byte, 0, numPackets)
	off := 12
	for i := 0; i < numPackets; i++ {
		if off+4 > len(data) {
			return nil, fmt.Errorf("truncated CBR oracle output at packet %d length", i)
		}
		plen := int(binary.LittleEndian.Uint32(data[off:]))
		off += 4
		if off+plen > len(data) {
			return nil, fmt.Errorf("truncated CBR oracle output at packet %d data", i)
		}
		packets = append(packets, append([]byte(nil), data[off:off+plen]...))
		off += plen
	}
	if off != len(data) {
		return nil, fmt.Errorf("CBR oracle output has %d trailing bytes", len(data)-off)
	}
	return packets, nil
}

// cbrTestCase describes one cell in the CBR parity matrix.
type cbrTestCase struct {
	name string
	// gopus encoding parameters
	gopusMode encoder.Mode // mode to force in gopus (ModeHybrid uses ModeAuto)
	bandwidth types.Bandwidth
	channels  int
	bitrate   int
	frameSize int // samples at 48 kHz
	// oracle parameters
	oracleApp uint32 // application code
	oracleBW  uint32 // bandwidth constant
	// behavior flags
	// byteExact: true on amd64 (CELT integer path exact); false on arm64
	// (arm64 CELT FMA drift is documented in project_arm64_celt_1ulp_drift.md)
	strictArm64 bool // if false, arm64 diffs are logged but not fatal
}

// cbrTestMatrix returns the CBR × mode × rate × frame × channel test matrix.
// All SILK cells are byte-exact on all platforms.
// CELT/Hybrid cells on arm64 use the arm64-1ULP-drift residual policy.
func cbrTestMatrix() []cbrTestCase {
	return []cbrTestCase{
		// --- SILK ---
		// SILK is pure fixed-point arithmetic; identical on all platforms.
		{
			name:      "SILK-NB-10ms-mono-16k",
			gopusMode: encoder.ModeSILK, bandwidth: types.BandwidthNarrowband,
			channels: 1, bitrate: 16000, frameSize: 480,
			oracleApp: cbrOracleAppRestrictedSilk, oracleBW: cbrOracleBWNarrowband,
			strictArm64: true,
		},
		{
			name:      "SILK-NB-20ms-mono-16k",
			gopusMode: encoder.ModeSILK, bandwidth: types.BandwidthNarrowband,
			channels: 1, bitrate: 16000, frameSize: 960,
			oracleApp: cbrOracleAppRestrictedSilk, oracleBW: cbrOracleBWNarrowband,
			strictArm64: true,
		},
		{
			name:      "SILK-MB-20ms-mono-24k",
			gopusMode: encoder.ModeSILK, bandwidth: types.BandwidthMediumband,
			channels: 1, bitrate: 24000, frameSize: 960,
			oracleApp: cbrOracleAppRestrictedSilk, oracleBW: cbrOracleBWMediumband,
			strictArm64: true,
		},
		{
			name:      "SILK-WB-10ms-mono-32k",
			gopusMode: encoder.ModeSILK, bandwidth: types.BandwidthWideband,
			channels: 1, bitrate: 32000, frameSize: 480,
			oracleApp: cbrOracleAppRestrictedSilk, oracleBW: cbrOracleBWWideband,
			strictArm64: true,
		},
		{
			name:      "SILK-WB-20ms-mono-32k",
			gopusMode: encoder.ModeSILK, bandwidth: types.BandwidthWideband,
			channels: 1, bitrate: 32000, frameSize: 960,
			oracleApp: cbrOracleAppRestrictedSilk, oracleBW: cbrOracleBWWideband,
			strictArm64: true,
		},
		{
			name:      "SILK-WB-40ms-mono-32k",
			gopusMode: encoder.ModeSILK, bandwidth: types.BandwidthWideband,
			channels: 1, bitrate: 32000, frameSize: 1920,
			oracleApp: cbrOracleAppRestrictedSilk, oracleBW: cbrOracleBWWideband,
			strictArm64: true,
		},
		{
			name:      "SILK-WB-20ms-stereo-48k",
			gopusMode: encoder.ModeSILK, bandwidth: types.BandwidthWideband,
			channels: 2, bitrate: 48000, frameSize: 960,
			oracleApp: cbrOracleAppRestrictedSilk, oracleBW: cbrOracleBWWideband,
			strictArm64: true,
		},
		// --- CELT ---
		// CELT uses floating-point arithmetic.  On amd64 (CI) these are byte-exact.
		// On arm64 the CELT sub-band FMA differs from clang -ffp-contract=on by
		// at most 1 ULP per operation; diffs are reported as honest residuals.
		{
			name:      "CELT-FB-2p5ms-mono-64k",
			gopusMode: encoder.ModeCELT, bandwidth: types.BandwidthFullband,
			channels: 1, bitrate: 64000, frameSize: 120,
			oracleApp: cbrOracleAppRestrictedCELT, oracleBW: cbrOracleBWFullband,
			strictArm64: false,
		},
		// Stereo 2.5/5ms CBR byte parity — covers the variant-byte ratchet surface.
		{
			name:      "CELT-FB-2p5ms-stereo-128k",
			gopusMode: encoder.ModeCELT, bandwidth: types.BandwidthFullband,
			channels: 2, bitrate: 128000, frameSize: 120,
			oracleApp: cbrOracleAppRestrictedCELT, oracleBW: cbrOracleBWFullband,
			strictArm64: false,
		},
		{
			name:      "CELT-FB-5ms-mono-64k",
			gopusMode: encoder.ModeCELT, bandwidth: types.BandwidthFullband,
			channels: 1, bitrate: 64000, frameSize: 240,
			oracleApp: cbrOracleAppRestrictedCELT, oracleBW: cbrOracleBWFullband,
			strictArm64: false,
		},
		{
			name:      "CELT-FB-5ms-stereo-128k",
			gopusMode: encoder.ModeCELT, bandwidth: types.BandwidthFullband,
			channels: 2, bitrate: 128000, frameSize: 240,
			oracleApp: cbrOracleAppRestrictedCELT, oracleBW: cbrOracleBWFullband,
			strictArm64: false,
		},
		{
			name:      "CELT-FB-10ms-mono-64k",
			gopusMode: encoder.ModeCELT, bandwidth: types.BandwidthFullband,
			channels: 1, bitrate: 64000, frameSize: 480,
			oracleApp: cbrOracleAppRestrictedCELT, oracleBW: cbrOracleBWFullband,
			strictArm64: false,
		},
		{
			name:      "CELT-FB-20ms-mono-64k",
			gopusMode: encoder.ModeCELT, bandwidth: types.BandwidthFullband,
			channels: 1, bitrate: 64000, frameSize: 960,
			oracleApp: cbrOracleAppRestrictedCELT, oracleBW: cbrOracleBWFullband,
			strictArm64: false,
		},
		{
			name:      "CELT-FB-20ms-stereo-128k",
			gopusMode: encoder.ModeCELT, bandwidth: types.BandwidthFullband,
			channels: 2, bitrate: 128000, frameSize: 960,
			oracleApp: cbrOracleAppRestrictedCELT, oracleBW: cbrOracleBWFullband,
			strictArm64: false,
		},
		// --- Hybrid ---
		// Hybrid uses ModeAuto (audio application) to match opus_demo -e audio.
		// The CELT sub-band carries the same arm64 FMA residual as CELT-only.
		{
			name:      "Hybrid-SWB-10ms-mono-48k",
			gopusMode: encoder.ModeHybrid, bandwidth: types.BandwidthSuperwideband,
			channels: 1, bitrate: 48000, frameSize: 480,
			oracleApp: cbrOracleAppAudio, oracleBW: cbrOracleBWSuperWideband,
			strictArm64: false,
		},
		{
			name:      "Hybrid-SWB-20ms-mono-48k",
			gopusMode: encoder.ModeHybrid, bandwidth: types.BandwidthSuperwideband,
			channels: 1, bitrate: 48000, frameSize: 960,
			oracleApp: cbrOracleAppAudio, oracleBW: cbrOracleBWSuperWideband,
			strictArm64: false,
		},
		{
			name:      "Hybrid-FB-10ms-mono-64k",
			gopusMode: encoder.ModeHybrid, bandwidth: types.BandwidthFullband,
			channels: 1, bitrate: 64000, frameSize: 480,
			oracleApp: cbrOracleAppAudio, oracleBW: cbrOracleBWFullband,
			strictArm64: false,
		},
		{
			name:      "Hybrid-FB-20ms-mono-64k",
			gopusMode: encoder.ModeHybrid, bandwidth: types.BandwidthFullband,
			channels: 1, bitrate: 64000, frameSize: 960,
			oracleApp: cbrOracleAppAudio, oracleBW: cbrOracleBWFullband,
			strictArm64: false,
		},
		{
			name:      "Hybrid-FB-20ms-stereo-96k",
			gopusMode: encoder.ModeHybrid, bandwidth: types.BandwidthFullband,
			channels: 2, bitrate: 96000, frameSize: 960,
			oracleApp: cbrOracleAppAudio, oracleBW: cbrOracleBWFullband,
			strictArm64: false,
		},
	}
}

// encodeGopusCBR encodes PCM with gopus in CBR mode, mirroring the oracle setup.
// Hybrid rows use ModeAuto (matching opus_demo -e audio).
// CELT rows use SetLowDelay(true) (matching opus_demo -e restricted-celt).
func encodeGopusCBR(tc cbrTestCase, pcm []float32) ([][]byte, error) {
	enc := encoder.NewEncoder(48000, tc.channels)
	gopusMode := tc.gopusMode
	if tc.gopusMode == encoder.ModeHybrid {
		// opus_demo -e audio uses adaptive mode selection.
		gopusMode = encoder.ModeAuto
	}
	enc.SetMode(gopusMode)
	// opus_demo -e restricted-celt disables the top-level delay buffer.
	enc.SetLowDelay(tc.gopusMode == encoder.ModeCELT)
	enc.SetBandwidth(tc.bandwidth)
	enc.SetBitrate(tc.bitrate)
	enc.SetBitrateMode(encoder.ModeCBR)
	enc.SetComplexity(10)

	samplesPerFrame := tc.frameSize * tc.channels
	numFrames := len(pcm) / samplesPerFrame
	packets := make([][]byte, 0, numFrames)

	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		// Mirror opus_demo -f32 input quantization: floor(0.5 + sample*8388608) / 8388608
		// This keeps parity aligned with how the libopus oracle reads its PCM input.
		// Reference: libopus src/opus_demo.c read_float() -f32 path
		frame := float32ToFloat64OpusDemoF32(pcm[start:end])
		pkt, err := encodeTest(enc, frame, tc.frameSize)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		if len(pkt) == 0 {
			return nil, fmt.Errorf("frame %d produced empty packet", i)
		}
		cp := make([]byte, len(pkt))
		copy(cp, pkt)
		packets = append(packets, cp)
	}
	return packets, nil
}

// runCBROracleEncode calls the libopus CBR encoder oracle.
// PCM must be float32 LE samples with no -f32 quantization applied —
// the oracle reads raw float32 values directly via fread() and passes
// them to opus_encode_float() without quantization.
func runCBROracleEncode(oraclePath string, tc cbrTestCase, pcm []float32) ([][]byte, error) {
	numFrames := uint32(len(pcm) / (tc.frameSize * tc.channels))
	// The oracle receives the raw float32 PCM (not quantized).
	// gopus encodes with float32ToFloat64OpusDemoF32 quantization applied;
	// to stay aligned we must feed the oracle the SAME post-quantization samples.
	// The oracle calls opus_encode_float() which accepts float32; convert back:
	quantPCM := make([]float32, len(pcm))
	for i, s := range pcm {
		q := math.Floor(0.5+float64(s)*8388608.0) / 8388608.0
		quantPCM[i] = float32(q)
	}
	input := cbrOracleInput(
		tc.oracleApp, tc.oracleBW,
		uint32(tc.channels), uint32(tc.bitrate),
		uint32(tc.frameSize), 10, numFrames, quantPCM,
	)
	out, err := libopustest.RunHelper(oraclePath, input)
	if err != nil {
		return nil, fmt.Errorf("oracle run: %w", err)
	}
	return parseCBROracleOutput(out)
}

// reportCBRByteDiff reports the first N mismatching frames with byte diffs.
func reportCBRByteDiff(t *testing.T, frameIdx int, got, want []byte) {
	t.Helper()
	limit := len(got)
	if len(want) < limit {
		limit = len(want)
	}
	first := -1
	for i := 0; i < limit; i++ {
		if got[i] != want[i] {
			first = i
			break
		}
	}
	if first < 0 && len(got) != len(want) {
		first = limit
	}
	t.Logf("  frame %d DIVERGES len(got=%d want=%d) firstByteDiff=%d", frameIdx, len(got), len(want), first)
	if first >= 0 {
		start := first - 2
		if start < 0 {
			start = 0
		}
		end := first + 8
		if end > len(got) {
			end = len(got)
		}
		wantEnd := end
		if wantEnd > len(want) {
			wantEnd = len(want)
		}
		t.Logf("    got [%d:%d]=%x", start, end, got[start:end])
		t.Logf("    want[%d:%d]=%x", start, wantEnd, want[start:wantEnd])
	}
}

// assertCBRByteParityForCase is the inner assertion for one CBR matrix cell.
func assertCBRByteParityForCase(t *testing.T, tc cbrTestCase, oraclePath string) {
	t.Helper()

	numFrames := 48000 / tc.frameSize
	totalSamples := numFrames * tc.frameSize * tc.channels
	pcm, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1, 48000, totalSamples, tc.channels,
	)
	if err != nil {
		t.Fatalf("generate signal: %v", err)
	}

	wantPackets, err := runCBROracleEncode(oraclePath, tc, pcm)
	if err != nil {
		libopustest.HelperUnavailable(t, "CBR encode oracle", err)
		return
	}
	gotPackets, err := encodeGopusCBR(tc, pcm)
	if err != nil {
		t.Fatalf("gopus CBR encode: %v", err)
	}

	if len(gotPackets) != len(wantPackets) {
		t.Fatalf("packet count: got=%d want=%d", len(gotPackets), len(wantPackets))
	}

	var diffFrames []int
	for i := range wantPackets {
		if !bytes.Equal(gotPackets[i], wantPackets[i]) {
			diffFrames = append(diffFrames, i)
		}
	}

	// SILK cells (tc.strictArm64) are byte-exact on every build (integer/
	// range-coded core). CELT/Hybrid cells carry the documented ≤1-ULP CELT
	// float-analysis boundary on the pure-Go builds (arm64 FMA, amd64-purego vs
	// scalar libopus); only the amd64 asm/SIMD build is held strictly bit-exact.
	// See encoderCELTFloatBoundaryBuild and project_arm64_celt_1ulp_drift.md.
	strict := tc.strictArm64 || !encoderCELTFloatBoundaryBuild()

	if len(diffFrames) == 0 {
		t.Logf("PASS: %d packets byte-exact vs libopus CBR oracle", len(wantPackets))
		return
	}

	// Report the first few diffs (max 3) for diagnosis
	for i, fi := range diffFrames {
		if i >= 3 {
			t.Logf("  ... and %d more differing frames", len(diffFrames)-3)
			break
		}
		reportCBRByteDiff(t, fi, gotPackets[fi], wantPackets[fi])
	}

	if strict {
		t.Fatalf("CBR byte parity FAIL: %d/%d packets differ (arch=%s/%s)",
			len(diffFrames), len(wantPackets), runtime.GOOS, runtime.GOARCH)
	} else {
		// Pure-Go CELT/Hybrid residual: documented ≤1-ULP CELT float boundary
		// (arm64 FMA contraction vs clang -ffp-contract=on; amd64-purego Go float
		// vs gcc scalar libopus). The CBR byte budget is fixed, so a near-tie flip
		// changes only the late raw bits at an equal length — a structural
		// regression that changes a packet length still fails hard below.
		// See project_arm64_celt_1ulp_drift.md.
		for _, fi := range diffFrames {
			if len(gotPackets[fi]) != len(wantPackets[fi]) {
				t.Fatalf("CBR packet LENGTH mismatch frame %d: gopus=%d libopus=%d (arch=%s/%s) — "+
					"a CBR length divergence is structural, not the ≤1-ULP float boundary",
					fi, len(gotPackets[fi]), len(wantPackets[fi]), runtime.GOOS, runtime.GOARCH)
			}
		}
		t.Logf("RESIDUAL (pure-Go CELT float boundary): %d/%d packets differ in late raw bits "+
			"(equal length) — project_arm64_celt_1ulp_drift.md; amd64 asm/CI gate holds",
			len(diffFrames), len(wantPackets))
	}
}

// TestEncoderCBRByteParitySILK asserts byte-exact CBR packets for all SILK cells.
// SILK uses fixed-point arithmetic only; all platforms must produce identical output.
func TestEncoderCBRByteParitySILK(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	oraclePath, err := cbrEncoderOraclePath()
	if err != nil {
		libopustest.HelperUnavailable(t, "CBR encode oracle", err)
		return
	}

	for _, tc := range cbrTestMatrix() {
		if tc.gopusMode != encoder.ModeSILK {
			continue
		}
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertCBRByteParityForCase(t, tc, oraclePath)
		})
	}
}

// TestEncoderCBRByteParityCELT asserts byte-exact CBR packets for CELT cells.
// On amd64 (CI) all CELT cells must be byte-exact.
// On arm64 diffs within the CELT float FMA residual budget are reported but not fatal.
func TestEncoderCBRByteParityCELT(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	oraclePath, err := cbrEncoderOraclePath()
	if err != nil {
		libopustest.HelperUnavailable(t, "CBR encode oracle", err)
		return
	}

	for _, tc := range cbrTestMatrix() {
		if tc.gopusMode != encoder.ModeCELT {
			continue
		}
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertCBRByteParityForCase(t, tc, oraclePath)
		})
	}
}

// TestEncoderCBRByteParityHybrid asserts byte-exact CBR packets for Hybrid cells.
// On amd64 (CI) Hybrid must be byte-exact (SILK part is exact; CELT part is exact on amd64).
// On arm64 the CELT sub-band may show ≤1 ULP drift.
func TestEncoderCBRByteParityHybrid(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	oraclePath, err := cbrEncoderOraclePath()
	if err != nil {
		libopustest.HelperUnavailable(t, "CBR encode oracle", err)
		return
	}

	for _, tc := range cbrTestMatrix() {
		if tc.gopusMode != encoder.ModeHybrid {
			continue
		}
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertCBRByteParityForCase(t, tc, oraclePath)
		})
	}
}

// TestEncoderCBRByteParitySummary runs the full CBR matrix and prints a summary table.
func TestEncoderCBRByteParitySummary(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	oraclePath, err := cbrEncoderOraclePath()
	if err != nil {
		libopustest.HelperUnavailable(t, "CBR encode oracle", err)
		return
	}

	type rowResult struct {
		name          string
		total         int
		diffs         int
		skipped       bool
		floatBoundary bool
		strictArm64   bool
	}
	results := make([]rowResult, len(cbrTestMatrix()))

	t.Run("cases", func(t *testing.T) {
		for i, tc := range cbrTestMatrix() {
			i, tc := i, tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				numFrames := 48000 / tc.frameSize
				totalSamples := numFrames * tc.frameSize * tc.channels
				pcm, err := testsignal.GenerateEncoderSignalVariant(
					testsignal.EncoderVariantAMMultisineV1, 48000, totalSamples, tc.channels,
				)
				if err != nil {
					t.Fatalf("generate signal: %v", err)
				}

				wantPackets, err := runCBROracleEncode(oraclePath, tc, pcm)
				if err != nil {
					results[i] = rowResult{name: tc.name, skipped: true}
					t.Skipf("oracle unavailable: %v", err)
					return
				}
				gotPackets, err := encodeGopusCBR(tc, pcm)
				if err != nil {
					t.Fatalf("gopus CBR encode: %v", err)
				}

				diffs := 0
				if len(gotPackets) != len(wantPackets) {
					diffs = len(wantPackets)
				} else {
					for j := range wantPackets {
						if !bytes.Equal(gotPackets[j], wantPackets[j]) {
							diffs++
						}
					}
				}

				floatBoundary := encoderCELTFloatBoundaryBuild()
				results[i] = rowResult{
					name:          tc.name,
					total:         len(wantPackets),
					diffs:         diffs,
					floatBoundary: floatBoundary,
					strictArm64:   tc.strictArm64,
				}

				// SILK (strictArm64) is byte-exact on every build; CELT/Hybrid carry
				// the documented ≤1-ULP CELT float boundary on the pure-Go builds, so
				// only the amd64 asm/SIMD build holds them strictly bit-exact.
				strict := tc.strictArm64 || !floatBoundary
				if diffs > 0 && strict {
					t.Errorf("%s: %d/%d packets differ (FAIL)", tc.name, diffs, len(wantPackets))
				}
			})
		}
	})

	t.Log("CBR Byte Parity Summary")
	t.Log("=======================")
	t.Logf("%-35s %7s %7s %6s", "Case", "Total", "Diffs", "Status")
	t.Logf("%-35s %7s %7s %6s", "----", "-----", "-----", "------")

	pass, fail, residual, skipped := 0, 0, 0, 0
	for _, r := range results {
		switch {
		case r.skipped:
			t.Logf("%-35s %7s %7s %6s", r.name, "-", "-", "SKIP")
			skipped++
		case r.diffs == 0:
			t.Logf("%-35s %7d %7d %6s", r.name, r.total, 0, "OK")
			pass++
		case r.floatBoundary && !r.strictArm64:
			t.Logf("%-35s %7d %7d %6s (pure-Go CELT float residual)", r.name, r.total, r.diffs, "~")
			residual++
		default:
			t.Logf("%-35s %7d %7d %6s", r.name, r.total, r.diffs, "FAIL")
			fail++
		}
	}
	t.Logf("---")
	t.Logf("pass=%d residual=%d fail=%d skip=%d  arch=%s/%s",
		pass, residual, fail, skipped, runtime.GOOS, runtime.GOARCH)
}
