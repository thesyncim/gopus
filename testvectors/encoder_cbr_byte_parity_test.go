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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
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
	if _, err := libopustooling.ResolveLibopusReferenceVariant(); err != nil {
		return "", err
	}
	return cbrOracleHelperCache.CHelperPath(libopustest.CHelperConfig{
		Label:      "CBR encode",
		OutputBase: "gopus_libopus_cbr_encode_packets",
		SourceFile: "libopus_cbr_encode_packets.c",
		CFlags:     []string{"-DHAVE_CONFIG_H"},
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

const (
	cbrFeatureRTCD               uint32 = 1 << 0
	cbrFeatureX86MaySSE          uint32 = 1 << 1
	cbrFeatureX86MaySSE2         uint32 = 1 << 2
	cbrFeatureX86MaySSE41        uint32 = 1 << 3
	cbrFeatureX86MayAVX2         uint32 = 1 << 4
	cbrFeatureX86PresumeSSE      uint32 = 1 << 5
	cbrFeatureX86PresumeSSE2     uint32 = 1 << 6
	cbrFeatureX86PresumeSSE41    uint32 = 1 << 7
	cbrFeatureX86PresumeAVX2     uint32 = 1 << 8
	cbrFeatureARMMayNEON         uint32 = 1 << 9
	cbrFeatureARMPresumeNEON     uint32 = 1 << 10
	cbrFeatureARMMayNEONIntr     uint32 = 1 << 11
	cbrFeatureARMPresumeNEONIntr uint32 = 1 << 12
	cbrFeatureARMMayDotprod      uint32 = 1 << 13
	cbrFeatureARMPresumeDotprod  uint32 = 1 << 14
)

type cbrOracleOutput struct {
	LibopusVersion string
	ArchMask       uint32
	BuildFeatures  uint32
	SelectedArch   uint32
	Packets        [][]byte
	FinalRanges    []uint32
}

type cbrReferenceStamp struct {
	Variant libopustooling.LibopusReferenceVariant
	Path    string
	Digest  string
	Fields  map[string]string
}

type cbrEncodedOutput struct {
	Packets     [][]byte
	FinalRanges []uint32
}

// parseCBROracleOutput parses the libopus CBR oracle output.
// Version 2 reports the compiled libopus configuration and runtime-selected
// architecture, then one packet and final range for every input frame.
func parseCBROracleOutput(data []byte) (cbrOracleOutput, error) {
	var result cbrOracleOutput
	if len(data) < 8 || string(data[:4]) != "GCBO" {
		preview := min(len(data), 4)
		return result, fmt.Errorf("bad CBR oracle output magic (got %q)", data[:preview])
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != 2 {
		return result, fmt.Errorf("CBR oracle output version=%d want 2", version)
	}
	off := 8
	if off+4 > len(data) {
		return result, fmt.Errorf("truncated CBR oracle output at version string length")
	}
	versionLen := int(binary.LittleEndian.Uint32(data[off:]))
	off += 4
	if versionLen == 0 || versionLen > 128 || off+versionLen > len(data) {
		return result, fmt.Errorf("invalid CBR oracle libopus version string length %d", versionLen)
	}
	result.LibopusVersion = string(data[off : off+versionLen])
	off += versionLen
	if off+16 > len(data) {
		return result, fmt.Errorf("truncated CBR oracle build metadata")
	}
	result.ArchMask = binary.LittleEndian.Uint32(data[off:])
	result.BuildFeatures = binary.LittleEndian.Uint32(data[off+4:])
	result.SelectedArch = binary.LittleEndian.Uint32(data[off+8:])
	numFrames := int(binary.LittleEndian.Uint32(data[off+12:]))
	off += 16
	if numFrames > (len(data)-off)/8 {
		return result, fmt.Errorf("CBR oracle frame count %d exceeds remaining output", numFrames)
	}
	result.Packets = make([][]byte, 0, numFrames)
	result.FinalRanges = make([]uint32, 0, numFrames)
	for i := range numFrames {
		if off+8 > len(data) {
			return cbrOracleOutput{}, fmt.Errorf("truncated CBR oracle output at frame %d metadata", i)
		}
		plen := uint64(binary.LittleEndian.Uint32(data[off:]))
		finalRange := binary.LittleEndian.Uint32(data[off+4:])
		off += 8
		if plen > uint64(len(data)-off) {
			return cbrOracleOutput{}, fmt.Errorf("truncated CBR oracle output at frame %d packet data", i)
		}
		end := off + int(plen)
		result.Packets = append(result.Packets, append([]byte(nil), data[off:end]...))
		result.FinalRanges = append(result.FinalRanges, finalRange)
		off = end
	}
	if off != len(data) {
		return cbrOracleOutput{}, fmt.Errorf("CBR oracle output has %d trailing bytes", len(data)-off)
	}
	return result, nil
}

func cbrReferenceBuildStamp() (cbrReferenceStamp, error) {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return cbrReferenceStamp{}, err
	}
	stampPath := libopustest.RefPath(".gopus-libopus-build")
	refDir := filepath.Dir(stampPath)
	if err := libopustooling.ValidateLibopusReferenceBuild(refDir, variant, libopustooling.DefaultVersion); err != nil {
		return cbrReferenceStamp{}, err
	}
	data, err := os.ReadFile(stampPath)
	if err != nil {
		return cbrReferenceStamp{}, fmt.Errorf("read paired libopus build stamp: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if len(lines) < 2 || lines[0] != "gopus libopus helper build v5" {
		return cbrReferenceStamp{}, fmt.Errorf("malformed paired libopus build stamp %s", stampPath)
	}
	fields := make(map[string]string, len(lines)-1)
	for _, line := range lines[1:] {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return cbrReferenceStamp{}, fmt.Errorf("malformed paired libopus build stamp line %q", line)
		}
		fields[key] = value
	}
	for _, key := range []string{"version", "configure", "CFLAGS", "cc_target", "cc_version"} {
		if strings.TrimSpace(fields[key]) == "" {
			return cbrReferenceStamp{}, fmt.Errorf("paired libopus build stamp has no %s", key)
		}
	}
	digest := sha256.Sum256(data)
	return cbrReferenceStamp{
		Variant: variant,
		Path:    stampPath,
		Digest:  hex.EncodeToString(digest[:]),
		Fields:  fields,
	}, nil
}

func validateCBROracleDispatch(output cbrOracleOutput, variant libopustooling.LibopusReferenceVariant, goarch string) error {
	if !strings.Contains(output.LibopusVersion, libopustooling.DefaultVersion) {
		return fmt.Errorf("C helper reports libopus version %q, want %s", output.LibopusVersion, libopustooling.DefaultVersion)
	}
	const knownFeatures = cbrFeatureRTCD | cbrFeatureX86MaySSE | cbrFeatureX86MaySSE2 | cbrFeatureX86MaySSE41 | cbrFeatureX86MayAVX2 |
		cbrFeatureX86PresumeSSE | cbrFeatureX86PresumeSSE2 | cbrFeatureX86PresumeSSE41 | cbrFeatureX86PresumeAVX2 |
		cbrFeatureARMMayNEON | cbrFeatureARMPresumeNEON | cbrFeatureARMMayNEONIntr | cbrFeatureARMPresumeNEONIntr |
		cbrFeatureARMMayDotprod | cbrFeatureARMPresumeDotprod
	if unknown := output.BuildFeatures &^ knownFeatures; unknown != 0 {
		return fmt.Errorf("C helper reports unknown build feature bits 0x%x", unknown)
	}
	if output.SelectedArch > output.ArchMask {
		return fmt.Errorf("C helper selected architecture %d above OPUS_ARCHMASK %d", output.SelectedArch, output.ArchMask)
	}
	if variant == libopustooling.LibopusReferenceScalar {
		if output.ArchMask != 0 || output.BuildFeatures != 0 || output.SelectedArch != 0 {
			return fmt.Errorf("Go scalar build paired with C SIMD metadata: arch_mask=%d features=%s selected_arch=%d", output.ArchMask, cbrFeatureNames(output.BuildFeatures), output.SelectedArch)
		}
		return nil
	}
	if variant != libopustooling.LibopusReferenceSIMD {
		return fmt.Errorf("unsupported paired CBR reference variant %q", variant)
	}
	var instructionFeatures uint32
	switch goarch {
	case "amd64":
		instructionFeatures = cbrFeatureX86MaySSE | cbrFeatureX86MaySSE2 | cbrFeatureX86MaySSE41 | cbrFeatureX86MayAVX2 |
			cbrFeatureX86PresumeSSE | cbrFeatureX86PresumeSSE2 | cbrFeatureX86PresumeSSE41 | cbrFeatureX86PresumeAVX2
	case "arm64":
		instructionFeatures = cbrFeatureARMMayNEON | cbrFeatureARMPresumeNEON | cbrFeatureARMMayNEONIntr | cbrFeatureARMPresumeNEONIntr | cbrFeatureARMMayDotprod | cbrFeatureARMPresumeDotprod
	default:
		return fmt.Errorf("Go SIMD pairing is unsupported on GOARCH=%s", goarch)
	}
	if output.BuildFeatures&instructionFeatures == 0 {
		return fmt.Errorf("%s Go SIMD build paired with C config lacking native SIMD features: %s", goarch, cbrFeatureNames(output.BuildFeatures))
	}
	if output.ArchMask == 0 {
		// A presumed native kernel has no runtime choice; its generated config
		// still reports the instruction family used by libopus.
		if output.BuildFeatures&cbrFeatureRTCD != 0 {
			return fmt.Errorf("C config reports RTCD with OPUS_ARCHMASK=0")
		}
		var presumedFeatures uint32
		switch goarch {
		case "amd64":
			presumedFeatures = cbrFeatureX86PresumeSSE | cbrFeatureX86PresumeSSE2 | cbrFeatureX86PresumeSSE41 | cbrFeatureX86PresumeAVX2
		case "arm64":
			presumedFeatures = cbrFeatureARMPresumeNEON | cbrFeatureARMPresumeNEONIntr | cbrFeatureARMPresumeDotprod
		}
		if output.BuildFeatures&presumedFeatures == 0 {
			return fmt.Errorf("C SIMD config has no presumed native kernel: %s", cbrFeatureNames(output.BuildFeatures))
		}
		return nil
	}
	if output.BuildFeatures&cbrFeatureRTCD == 0 || output.SelectedArch == 0 {
		return fmt.Errorf("C SIMD config does not select a native runtime path: arch_mask=%d features=%s selected_arch=%d", output.ArchMask, cbrFeatureNames(output.BuildFeatures), output.SelectedArch)
	}
	return nil
}

func cbrFeatureNames(bits uint32) []string {
	names := make([]string, 0, 15)
	for _, feature := range []struct {
		bit  uint32
		name string
	}{
		{cbrFeatureRTCD, "rtcd"},
		{cbrFeatureX86MaySSE, "x86-may-sse"},
		{cbrFeatureX86MaySSE2, "x86-may-sse2"},
		{cbrFeatureX86MaySSE41, "x86-may-sse4.1"},
		{cbrFeatureX86MayAVX2, "x86-may-avx2"},
		{cbrFeatureX86PresumeSSE, "x86-presume-sse"},
		{cbrFeatureX86PresumeSSE2, "x86-presume-sse2"},
		{cbrFeatureX86PresumeSSE41, "x86-presume-sse4.1"},
		{cbrFeatureX86PresumeAVX2, "x86-presume-avx2"},
		{cbrFeatureARMMayNEON, "arm-may-neon"},
		{cbrFeatureARMPresumeNEON, "arm-presume-neon"},
		{cbrFeatureARMMayNEONIntr, "arm-may-neon-intr"},
		{cbrFeatureARMPresumeNEONIntr, "arm-presume-neon-intr"},
		{cbrFeatureARMMayDotprod, "arm-may-dotprod"},
		{cbrFeatureARMPresumeDotprod, "arm-presume-dotprod"},
	} {
		if bits&feature.bit != 0 {
			names = append(names, feature.name)
		}
	}
	return names
}

func TestParseCBROracleOutputRejectsBadV2Format(t *testing.T) {
	makeHeader := func(version uint32) []byte {
		data := append([]byte(nil), []byte("GCBO")...)
		var word [4]byte
		binary.LittleEndian.PutUint32(word[:], version)
		data = append(data, word[:]...)
		if version == 2 {
			binary.LittleEndian.PutUint32(word[:], uint32(len("libopus 1.6.1")))
			data = append(data, word[:]...)
			data = append(data, "libopus 1.6.1"...)
			for range 4 {
				binary.LittleEndian.PutUint32(word[:], 0)
				data = append(data, word[:]...)
			}
		}
		return data
	}
	valid := makeHeader(2)
	if got, err := parseCBROracleOutput(valid); err != nil || got.LibopusVersion != "libopus 1.6.1" || len(got.Packets) != 0 {
		t.Fatalf("valid v2 header parse=(%+v,%v)", got, err)
	}
	for name, data := range map[string][]byte{
		"legacy version":     makeHeader(1),
		"truncated metadata": valid[:len(valid)-1],
		"trailing bytes":     append(append([]byte(nil), valid...), 0),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseCBROracleOutput(data); err == nil {
				t.Fatal("invalid CBR oracle output parsed without an error")
			}
		})
	}
}

func TestValidateCBROracleDispatchRejectsMismatchedBuilds(t *testing.T) {
	base := cbrOracleOutput{LibopusVersion: "libopus 1.6.1"}
	if err := validateCBROracleDispatch(base, libopustooling.LibopusReferenceScalar, "arm64"); err != nil {
		t.Fatalf("scalar metadata rejected: %v", err)
	}
	for name, tc := range map[string]struct {
		output  cbrOracleOutput
		variant libopustooling.LibopusReferenceVariant
		goarch  string
	}{
		"scalar with presumed NEON": {
			output:  cbrOracleOutput{LibopusVersion: "libopus 1.6.1", BuildFeatures: cbrFeatureARMPresumeNEONIntr},
			variant: libopustooling.LibopusReferenceScalar, goarch: "arm64",
		},
		"SIMD with scalar config": {
			output: base, variant: libopustooling.LibopusReferenceSIMD, goarch: "arm64",
		},
		"RTCD without architecture kernels": {
			output:  cbrOracleOutput{LibopusVersion: "libopus 1.6.1", ArchMask: 7, BuildFeatures: cbrFeatureRTCD, SelectedArch: 1},
			variant: libopustooling.LibopusReferenceSIMD, goarch: "arm64",
		},
		"SIMD unsupported architecture": {
			output:  cbrOracleOutput{LibopusVersion: "libopus 1.6.1", BuildFeatures: cbrFeatureARMPresumeNEONIntr},
			variant: libopustooling.LibopusReferenceSIMD, goarch: "riscv64",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCBROracleDispatch(tc.output, tc.variant, tc.goarch); err == nil {
				t.Fatal("mismatched CBR reference metadata accepted")
			}
		})
	}
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
func encodeGopusCBR(tc cbrTestCase, pcm []float32) (cbrEncodedOutput, error) {
	enc := encoder.NewEncoder(48000, tc.channels)
	gopusMode := tc.gopusMode
	if tc.gopusMode == encoder.ModeHybrid {
		// opus_demo -e audio uses adaptive mode selection.
		gopusMode = encoder.ModeAuto
	}
	enc.SetMode(gopusMode)
	enc.SetRestrictedSilkApplication(tc.gopusMode == encoder.ModeSILK)
	// opus_demo -e restricted-celt disables the top-level delay buffer.
	enc.SetLowDelay(tc.gopusMode == encoder.ModeCELT)
	enc.SetBandwidth(tc.bandwidth)
	enc.SetBitrate(tc.bitrate)
	enc.SetBitrateMode(encoder.ModeCBR)
	enc.SetComplexity(10)

	samplesPerFrame := tc.frameSize * tc.channels
	numFrames := len(pcm) / samplesPerFrame
	result := cbrEncodedOutput{
		Packets:     make([][]byte, 0, numFrames),
		FinalRanges: make([]uint32, 0, numFrames),
	}

	for i := range numFrames {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		// Mirror opus_demo -f32 input quantization: floor(0.5 + sample*8388608) / 8388608
		// This keeps parity aligned with how the libopus oracle reads its PCM input.
		// Reference: libopus src/opus_demo.c read_float() -f32 path
		frame := float32ToFloat64OpusDemoF32(pcm[start:end])
		pkt, err := encodeTest(enc, frame, tc.frameSize)
		if err != nil {
			return cbrEncodedOutput{}, fmt.Errorf("frame %d: %w", i, err)
		}
		if len(pkt) == 0 {
			return cbrEncodedOutput{}, fmt.Errorf("frame %d produced empty packet", i)
		}
		cp := make([]byte, len(pkt))
		copy(cp, pkt)
		result.Packets = append(result.Packets, cp)
		result.FinalRanges = append(result.FinalRanges, enc.FinalRange())
	}
	return result, nil
}

// runCBROracleEncode calls the libopus CBR encoder oracle.
// PCM must be float32 LE samples with no -f32 quantization applied —
// the oracle reads raw float32 values directly via fread() and passes
// them to opus_encode_float() without quantization.
func quantizeCBRPCM(pcm []float32) []float32 {
	quantized := make([]float32, len(pcm))
	for i, s := range pcm {
		q := math.Floor(0.5+float64(s)*8388608.0) / 8388608.0
		quantized[i] = float32(q)
	}
	return quantized
}

func cbrPCMIdentity(pcm []float32) string {
	quantized := quantizeCBRPCM(pcm)
	h := sha256.New()
	var bits [4]byte
	for _, sample := range quantized {
		binary.LittleEndian.PutUint32(bits[:], math.Float32bits(sample))
		_, _ = h.Write(bits[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func runCBROracleEncode(oraclePath string, tc cbrTestCase, pcm []float32) (cbrOracleOutput, error) {
	numFrames := uint32(len(pcm) / (tc.frameSize * tc.channels))
	// The oracle receives the raw float32 PCM (not quantized).
	// gopus encodes with float32ToFloat64OpusDemoF32 quantization applied;
	// to stay aligned we must feed the oracle the SAME post-quantization samples.
	// The oracle calls opus_encode_float() which accepts float32; convert back:
	quantPCM := quantizeCBRPCM(pcm)
	input := cbrOracleInput(
		tc.oracleApp, tc.oracleBW,
		uint32(tc.channels), uint32(tc.bitrate),
		uint32(tc.frameSize), 10, numFrames, quantPCM,
	)
	out, err := libopustest.RunHelper(oraclePath, input)
	if err != nil {
		return cbrOracleOutput{}, fmt.Errorf("oracle run: %w", err)
	}
	return parseCBROracleOutput(out)
}

// reportCBRByteDiff reports the first N mismatching frames with byte diffs.
func reportCBRByteDiff(t *testing.T, frameIdx int, got, want []byte) {
	t.Helper()
	limit := min(len(want), len(got))
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
		start := max(first-2, 0)
		end := min(first+8, len(got))
		wantEnd := min(end, len(want))
		t.Logf("    got [%d:%d]=%x", start, end, got[start:end])
		t.Logf("    want[%d:%d]=%x", start, wantEnd, want[start:wantEnd])
	}
}

func firstCBRByteDifference(got, want []byte) int {
	limit := min(len(got), len(want))
	for i := 0; i < limit; i++ {
		if got[i] != want[i] {
			return i
		}
	}
	if len(got) != len(want) {
		return limit
	}
	return -1
}

func cbrByteDiffSnippet(packet []byte, at int) (int, []byte) {
	start := max(at-2, 0)
	end := min(at+8, len(packet))
	return start, packet[start:end]
}

func TestEncoderCBRPairedOracleMatrixDefinition(t *testing.T) {
	const wantCases = 19
	if got := len(cbrTestMatrix()); got != wantCases {
		t.Fatalf("CBR matrix has %d cases, want %d", got, wantCases)
	}
}

// TestEncoderCBRPairedOracleExact compares every selected CBR packet and range
// state against the libopus variant selected for this Go build. A full run
// covers all 19 matrix rows; a filtered subtest reports its selected rows.
func TestEncoderCBRPairedOracleExact(t *testing.T) {
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	oraclePath, err := cbrEncoderOraclePath()
	if err != nil {
		t.Fatalf("build paired CBR oracle: %v", err)
	}
	stamp, err := cbrReferenceBuildStamp()
	if err != nil {
		t.Fatalf("resolve paired CBR reference: %v", err)
	}
	t.Logf("paired reference: variant=%s stamp=%s sha256=%s version=%s configure=%q CFLAGS=%q cc_target=%q cc=%q",
		stamp.Variant, stamp.Path, stamp.Digest, stamp.Fields["version"], stamp.Fields["configure"],
		stamp.Fields["CFLAGS"], stamp.Fields["cc_target"], stamp.Fields["cc_version"])

	caseCount, exactCases := 0, 0
	totalPackets, packetDiffs, rangeDiffs := 0, 0, 0
	var dispatch *cbrOracleOutput
	for _, tc := range cbrTestMatrix() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			caseCount++
			frameCount := 48000 / tc.frameSize
			pcm, err := testsignal.GenerateEncoderSignalVariant(
				testsignal.EncoderVariantAMMultisineV1, 48000, frameCount*tc.frameSize*tc.channels, tc.channels,
			)
			if err != nil {
				t.Fatalf("generate signal: %v", err)
			}
			inputID := cbrPCMIdentity(pcm)

			want, err := runCBROracleEncode(oraclePath, tc, pcm)
			if err != nil {
				t.Fatalf("run paired CBR oracle: %v", err)
			}
			if err := validateCBROracleDispatch(want, stamp.Variant, runtime.GOARCH); err != nil {
				t.Fatalf("paired CBR oracle metadata rejected: %v", err)
			}
			if dispatch == nil {
				dispatch = &cbrOracleOutput{
					LibopusVersion: want.LibopusVersion,
					ArchMask:       want.ArchMask,
					BuildFeatures:  want.BuildFeatures,
					SelectedArch:   want.SelectedArch,
				}
			}
			got, err := encodeGopusCBR(tc, pcm)
			if err != nil {
				t.Fatalf("gopus CBR encode: %v", err)
			}
			if len(got.Packets) != frameCount || len(got.FinalRanges) != frameCount {
				t.Fatalf("Go output frames=%d ranges=%d, want %d (input=%s)", len(got.Packets), len(got.FinalRanges), frameCount, inputID)
			}
			if len(want.Packets) != frameCount || len(want.FinalRanges) != frameCount {
				t.Fatalf("C output frames=%d ranges=%d, want %d (input=%s)", len(want.Packets), len(want.FinalRanges), frameCount, inputID)
			}

			casePacketDiffs, caseRangeDiffs := 0, 0
			firstPacketFrame, firstPacketByte, firstRangeFrame := -1, -1, -1
			for frame := 0; frame < frameCount; frame++ {
				totalPackets++
				if byteDiff := firstCBRByteDifference(got.Packets[frame], want.Packets[frame]); byteDiff >= 0 {
					casePacketDiffs++
					if firstPacketFrame < 0 {
						firstPacketFrame, firstPacketByte = frame, byteDiff
					}
				}
				if got.FinalRanges[frame] != want.FinalRanges[frame] {
					caseRangeDiffs++
					if firstRangeFrame < 0 {
						firstRangeFrame = frame
					}
				}
			}
			packetDiffs += casePacketDiffs
			rangeDiffs += caseRangeDiffs
			if casePacketDiffs != 0 || caseRangeDiffs != 0 {
				message := fmt.Sprintf("exact paired CBR mismatch: packets=%d/%d ranges=%d/%d input=AMMultisineV1/%s settings=mode:%d bandwidth:%d channels:%d bitrate:%d frame_size:%d complexity:10",
					casePacketDiffs, frameCount, caseRangeDiffs, frameCount, inputID,
					tc.gopusMode, tc.bandwidth, tc.channels, tc.bitrate, tc.frameSize)
				if firstPacketFrame >= 0 {
					goStart, goSnippet := cbrByteDiffSnippet(got.Packets[firstPacketFrame], firstPacketByte)
					cStart, cSnippet := cbrByteDiffSnippet(want.Packets[firstPacketFrame], firstPacketByte)
					message += fmt.Sprintf(" first_packet=frame:%d byte:%d go_len:%d c_len:%d go[%d]=%x c[%d]=%x",
						firstPacketFrame, firstPacketByte, len(got.Packets[firstPacketFrame]), len(want.Packets[firstPacketFrame]),
						goStart, goSnippet, cStart, cSnippet)
				}
				if firstRangeFrame >= 0 {
					message += fmt.Sprintf(" first_range=frame:%d go:0x%08x c:0x%08x",
						firstRangeFrame, got.FinalRanges[firstRangeFrame], want.FinalRanges[firstRangeFrame])
				}
				t.Errorf("%s", message)
				return
			}
			exactCases++
			t.Logf("EXACT: %d/%d frames; input=AMMultisineV1/%s", frameCount, frameCount, inputID)
		})
	}
	if dispatch != nil {
		t.Logf("C helper dispatch: version=%q OPUS_ARCHMASK=%d config_features=%v opus_select_arch=%d",
			dispatch.LibopusVersion, dispatch.ArchMask, cbrFeatureNames(dispatch.BuildFeatures), dispatch.SelectedArch)
	}
	t.Logf("strict paired CBR summary: variant=%s cases=%d exact_cases=%d packets=%d packet_diffs=%d range_diffs=%d",
		stamp.Variant, caseCount, exactCases, totalPackets, packetDiffs, rangeDiffs)
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

	wantOutput, err := runCBROracleEncode(oraclePath, tc, pcm)
	if err != nil {
		libopustest.HelperUnavailable(t, "CBR encode oracle", err)
		return
	}
	gotOutput, err := encodeGopusCBR(tc, pcm)
	if err != nil {
		t.Fatalf("gopus CBR encode: %v", err)
	}
	wantPackets := wantOutput.Packets
	gotPackets := gotOutput.Packets

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
	// float-analysis boundary on the pure-Go builds (arm64 FMA, amd64-nosimd vs
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
		// (arm64 FMA contraction vs clang -ffp-contract=on; amd64-nosimd Go float
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

				wantOutput, err := runCBROracleEncode(oraclePath, tc, pcm)
				if err != nil {
					results[i] = rowResult{name: tc.name, skipped: true}
					libopustest.HelperUnavailable(t, "CBR encode", err)
					return
				}
				gotOutput, err := encodeGopusCBR(tc, pcm)
				if err != nil {
					t.Fatalf("gopus CBR encode: %v", err)
				}
				wantPackets := wantOutput.Packets
				gotPackets := gotOutput.Packets

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
