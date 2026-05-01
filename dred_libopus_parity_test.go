//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusDREDParseInputMagic  = "GODI"
	libopusDREDParseOutputMagic = "GODO"
)

type libopusDREDParseInfo struct {
	availableSamples int
	dredEndSamples   int
}

type libopusDREDDecodeInfo struct {
	availableSamples int
	dredEndSamples   int
	processStage     int
	nbLatents        int
	dredOffset       int
	state            [internaldred.StateDim]float32
	latents          []float32
}

var (
	libopusDREDParseHelperOnce sync.Once
	libopusDREDParseHelperPath string
	libopusDREDParseHelperErr  error

	libopusDREDDecodeHelperOnce sync.Once
	libopusDREDDecodeHelperPath string
	libopusDREDDecodeHelperErr  error
)

func ensureLibopusDREDBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	referenceDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
	sourceDir = filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+"-dredsrc-clean")
	buildDir = filepath.Join(repoRoot, "tmp_check", fmt.Sprintf("build-opus-dred-scalar-%s-%s", runtime.GOOS, runtime.GOARCH))
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err == nil {
		return sourceDir, buildDir, nil
	}

	if _, err := os.Stat(filepath.Join(sourceDir, "configure")); err != nil {
		libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
		tarball := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+".tar.gz")
		if _, err := os.Stat(tarball); err == nil {
			if err := os.RemoveAll(sourceDir); err != nil {
				return "", "", fmt.Errorf("remove stale dred source dir: %w", err)
			}
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				return "", "", fmt.Errorf("mkdir dred source dir: %w", err)
			}
			cmd := exec.Command("tar", "-xzf", tarball, "-C", sourceDir, "--strip-components=1")
			if output, err := cmd.CombinedOutput(); err != nil {
				return "", "", fmt.Errorf("extract dred libopus source: %w (%s)", err, bytes.TrimSpace(output))
			}
		} else if _, refErr := os.Stat(filepath.Join(referenceDir, "configure")); refErr == nil {
			if _, cfgErr := os.Stat(filepath.Join(referenceDir, "Makefile")); cfgErr == nil {
				return "", "", fmt.Errorf("clean dred source tree unavailable: %s is already configured", referenceDir)
			}
			sourceDir = referenceDir
		} else {
			return "", "", fmt.Errorf("libopus tarball not found and no prepared source tree present: %w", err)
		}
	}

	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir dred build dir: %w", err)
	}

	if _, err := os.Stat(filepath.Join(buildDir, "Makefile")); err != nil {
		cmd := exec.Command(filepath.Join(sourceDir, "configure"),
			"--enable-static",
			"--disable-shared",
			"--disable-extra-programs",
			"--enable-dred",
			"--disable-asm",
			"--disable-rtcd",
			"--disable-intrinsics",
		)
		cmd.Dir = buildDir
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("configure dred libopus build: %w (%s)", err, bytes.TrimSpace(output))
		}
	}

	makeCmd := exec.Command("make", fmt.Sprintf("-j%d", max(1, runtime.NumCPU())))
	makeCmd.Dir = buildDir
	if output, err := makeCmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build dred libopus: %w (%s)", err, bytes.TrimSpace(output))
	}

	return sourceDir, buildDir, nil
}

func getLibopusDREDParseHelperPath() (string, error) {
	libopusDREDParseHelperOnce.Do(func() {
		libopusDREDParseHelperPath, libopusDREDParseHelperErr = buildLibopusDREDHelper("libopus_dred_parse_info.c", "gopus_libopus_dred_parse", false)
	})
	if libopusDREDParseHelperErr != nil {
		return "", libopusDREDParseHelperErr
	}
	return libopusDREDParseHelperPath, nil
}

func getLibopusDREDDecodeHelperPath() (string, error) {
	libopusDREDDecodeHelperOnce.Do(func() {
		libopusDREDDecodeHelperPath, libopusDREDDecodeHelperErr = buildLibopusDREDHelper("libopus_dred_decode_info.c", "gopus_libopus_dred_decode", true)
	})
	if libopusDREDDecodeHelperErr != nil {
		return "", libopusDREDDecodeHelperErr
	}
	return libopusDREDDecodeHelperPath, nil
}

func buildLibopusDREDHelper(sourceFile, outputBase string, includeInternal bool) (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	sourceDir, buildDir, err := ensureLibopusDREDBuild(repoRoot)
	if err != nil {
		return "", err
	}

	srcPath := filepath.Join(repoRoot, "tools", "csrc", sourceFile)
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("dred helper source not found: %w", err)
	}

	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err != nil {
		return "", fmt.Errorf("dred libopus static library not found: %w", err)
	}

	outPath := filepath.Join(buildDir, fmt.Sprintf("%s_%s_%s", outputBase, runtime.GOOS, runtime.GOARCH))
	if runtime.GOOS == "windows" {
		outPath += ".exe"
	}

	args := []string{
		"-std=c99",
		"-O2",
		"-DHAVE_CONFIG_H",
		"-I", buildDir,
		"-I", filepath.Join(sourceDir, "include"),
	}
	if includeInternal {
		args = append(args,
			"-I", sourceDir,
			"-I", filepath.Join(sourceDir, "src"),
			"-I", filepath.Join(sourceDir, "celt"),
			"-I", filepath.Join(sourceDir, "dnn"),
			"-I", filepath.Join(sourceDir, "silk"),
		)
	}
	args = append(args, srcPath, libopusStatic, "-lm", "-o", outPath)

	cmd := exec.Command(ccPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build dred helper %s: %w (%s)", sourceFile, err, bytes.TrimSpace(output))
	}
	return outPath, nil
}

func probeLibopusDREDParse(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDParseInfo, error) {
	binPath, err := getLibopusDREDParseHelperPath()
	if err != nil {
		return libopusDREDParseInfo{}, err
	}

	var payload bytes.Buffer
	payload.WriteString(libopusDREDParseInputMagic)
	for _, v := range []uint32{1, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet))} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusDREDParseInfo{}, fmt.Errorf("encode dred helper header: %w", err)
		}
	}
	if _, err := payload.Write(packet); err != nil {
		return libopusDREDParseInfo{}, fmt.Errorf("encode dred helper packet: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDREDParseInfo{}, fmt.Errorf("run dred helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	out := stdout.Bytes()
	if len(out) < 16 || string(out[:4]) != libopusDREDParseOutputMagic {
		return libopusDREDParseInfo{}, fmt.Errorf("unexpected dred helper output")
	}

	ret := int(int32(binary.LittleEndian.Uint32(out[8:12])))
	dredEnd := int(int32(binary.LittleEndian.Uint32(out[12:16])))
	return libopusDREDParseInfo{
		availableSamples: ret,
		dredEndSamples:   dredEnd,
	}, nil
}

func probeLibopusDREDDecode(packet []byte, maxDREDSamples, sampleRate int) (libopusDREDDecodeInfo, error) {
	binPath, err := getLibopusDREDDecodeHelperPath()
	if err != nil {
		return libopusDREDDecodeInfo{}, err
	}

	var payload bytes.Buffer
	payload.WriteString(libopusDREDParseInputMagic)
	for _, v := range []uint32{1, uint32(sampleRate), uint32(maxDREDSamples), uint32(len(packet))} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return libopusDREDDecodeInfo{}, fmt.Errorf("encode dred decode helper header: %w", err)
		}
	}
	if _, err := payload.Write(packet); err != nil {
		return libopusDREDDecodeInfo{}, fmt.Errorf("encode dred decode helper packet: %w", err)
	}

	cmd := exec.Command(binPath)
	cmd.Stdin = bytes.NewReader(payload.Bytes())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return libopusDREDDecodeInfo{}, fmt.Errorf("run dred decode helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}

	out := stdout.Bytes()
	headerBytes := 4 + 4 + 4 + 4 + 4 + 4 + 4
	if len(out) < headerBytes || string(out[:4]) != libopusDREDParseOutputMagic {
		return libopusDREDDecodeInfo{}, fmt.Errorf("unexpected dred decode helper output")
	}

	info := libopusDREDDecodeInfo{
		availableSamples: int(int32(binary.LittleEndian.Uint32(out[8:12]))),
		dredEndSamples:   int(int32(binary.LittleEndian.Uint32(out[12:16]))),
		processStage:     int(int32(binary.LittleEndian.Uint32(out[16:20]))),
		nbLatents:        int(int32(binary.LittleEndian.Uint32(out[20:24]))),
		dredOffset:       int(int32(binary.LittleEndian.Uint32(out[24:28]))),
	}

	offset := 28
	for i := range info.state {
		if len(out) < offset+4 {
			return libopusDREDDecodeInfo{}, fmt.Errorf("truncated dred decode helper state")
		}
		info.state[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
		offset += 4
	}

	latentValues := info.nbLatents * internaldred.LatentStride
	info.latents = make([]float32, latentValues)
	for i := 0; i < latentValues; i++ {
		if len(out) < offset+4 {
			return libopusDREDDecodeInfo{}, fmt.Errorf("truncated dred decode helper latents")
		}
		info.latents[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[offset : offset+4]))
		offset += 4
	}
	return info, nil
}

func TestParsedDREDAvailabilityMatchesLibopus(t *testing.T) {
	base := makeValidCELTPacketForDREDTest(t)
	if len(base) < 2 {
		t.Fatal("base packet too short")
	}

	twoFramePacket := make([]byte, len(base)*2+16)
	n, err := buildPacketWithOptions(base[0]&0xFC, [][]byte{base[1:], base[1:]}, twoFramePacket, 0, false, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 1, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 8, -4)...)},
	}, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}
	twoFramePacket = twoFramePacket[:n]

	tests := []struct {
		name           string
		packet         []byte
		maxDREDSamples int
	}{
		{
			name: "single_frame_offset_positive",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name:           "single_frame_offset_positive_large_request",
			packet:         buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)}}),
			maxDREDSamples: 10080,
		},
		{
			name:           "second_frame_negative_offset",
			packet:         twoFramePacket,
			maxDREDSamples: 960,
		},
		{
			name: "first_dred_extension_invalid_second_valid",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0x10}},
				{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, makeExperimentalDREDPayloadBodyForTest(t, 0, 4)...)},
			}),
			maxDREDSamples: 960,
		},
		{
			name: "short_experimental_payload",
			packet: buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
				{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'D', internaldred.ExperimentalVersion, 0xaa, 0xbb}},
			}),
			maxDREDSamples: 960,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload, frameOffset, ok, err := findDREDPayload(tc.packet)
			if err != nil {
				t.Fatalf("findDREDPayload error: %v", err)
			}
			if !ok {
				t.Fatal("findDREDPayload returned ok=false")
			}

			parsed, err := internaldred.ParsePayload(payload, frameOffset)
			if err != nil {
				t.Fatalf("ParsePayload error: %v", err)
			}
			got := parsed.Availability(tc.maxDREDSamples, 48000)

			want, err := probeLibopusDREDParse(tc.packet, tc.maxDREDSamples, 48000)
			if err != nil {
				t.Skipf("libopus dred helper unavailable: %v", err)
			}
			if want.availableSamples < 0 {
				t.Fatalf("libopus dred parse returned error %d", want.availableSamples)
			}

			if got.AvailableSamples != want.availableSamples {
				t.Fatalf("AvailableSamples=%d want %d", got.AvailableSamples, want.availableSamples)
			}
			if got.EndSamples != want.dredEndSamples {
				t.Fatalf("EndSamples=%d want %d", got.EndSamples, want.dredEndSamples)
			}

			span := internaldred.LatentSpanSamples(48000)
			if span <= 0 {
				t.Fatal("invalid latent span")
			}
			wantLatents := (want.availableSamples + got.OffsetSamples) / span
			if wantLatents < 0 {
				wantLatents = 0
			}
			if got.MaxLatents != wantLatents {
				t.Fatalf("MaxLatents=%d want %d", got.MaxLatents, wantLatents)
			}
		})
	}
}
