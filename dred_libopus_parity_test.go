package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
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

var (
	libopusDREDParseHelperOnce sync.Once
	libopusDREDParseHelperPath string
	libopusDREDParseHelperErr  error
)

func ensureLibopusDREDBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	sourceDir = filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+"-dredsrc-clean")
	buildDir = filepath.Join(repoRoot, "tmp_check", "build-opus-dred")
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err == nil {
		return sourceDir, buildDir, nil
	}

	tarball := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+".tar.gz")
	if _, err := os.Stat(tarball); err != nil {
		return "", "", fmt.Errorf("libopus tarball not found: %w", err)
	}

	if _, err := os.Stat(filepath.Join(sourceDir, "configure")); err != nil {
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
		ccPath, err := libopustooling.FindCCompiler()
		if err != nil {
			libopusDREDParseHelperErr = fmt.Errorf("cc not available: %w", err)
			return
		}

		repoRoot, err := os.Getwd()
		if err != nil {
			libopusDREDParseHelperErr = fmt.Errorf("getwd: %w", err)
			return
		}

		sourceDir, buildDir, err := ensureLibopusDREDBuild(repoRoot)
		if err != nil {
			libopusDREDParseHelperErr = err
			return
		}

		srcPath := filepath.Join(repoRoot, "tools", "csrc", "libopus_dred_parse_info.c")
		if _, err := os.Stat(srcPath); err != nil {
			libopusDREDParseHelperErr = fmt.Errorf("dred helper source not found: %w", err)
			return
		}

		libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
		if _, err := os.Stat(libopusStatic); err != nil {
			libopusDREDParseHelperErr = fmt.Errorf("dred libopus static library not found: %w", err)
			return
		}

		outPath := filepath.Join(buildDir, fmt.Sprintf("gopus_libopus_dred_parse_%s_%s", runtime.GOOS, runtime.GOARCH))
		if runtime.GOOS == "windows" {
			outPath += ".exe"
		}

		cmd := exec.Command(ccPath,
			"-std=c99",
			"-O2",
			"-I", filepath.Join(sourceDir, "include"),
			srcPath,
			libopusStatic,
			"-lm",
			"-o", outPath,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			libopusDREDParseHelperErr = fmt.Errorf("build dred parse helper: %w (%s)", err, bytes.TrimSpace(output))
			return
		}

		libopusDREDParseHelperPath = outPath
	})
	if libopusDREDParseHelperErr != nil {
		return "", libopusDREDParseHelperErr
	}
	return libopusDREDParseHelperPath, nil
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
