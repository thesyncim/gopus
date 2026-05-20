package libopustest

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

	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	FloatQuantModeFloat2Int16       = uint32(0)
	FloatQuantModeOSCEOutputScale   = uint32(1)
	FloatQuantModeFARGANSynthInt    = uint32(2)
	FloatQuantModeCELTRaw32767Round = uint32(3)
)

var (
	floatQuantHelperOnce sync.Once
	floatQuantHelperPath string
	floatQuantHelperErr  error
)

func EnsureDREDBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	referenceDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
	sourceDir = filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+"-dredsrc-clean")
	buildDir = filepath.Join(repoRoot, "tmp_check", fmt.Sprintf("build-opus-dred-scalar-%s-%s", runtime.GOOS, runtime.GOARCH))
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err == nil && libopustooling.ScalarDNNBuildIsCurrent(buildDir) {
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

	if err := libopustooling.ResetScalarDNNBuildIfStale(buildDir); err != nil {
		return "", "", fmt.Errorf("reset stale dred scalar build dir: %w", err)
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
		cmd.Env = libopustooling.ScalarDNNBuildEnv()
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("configure dred libopus build: %w (%s)", err, bytes.TrimSpace(output))
		}
	}

	makeCmd := exec.Command("make", fmt.Sprintf("-j%d", max(1, runtime.NumCPU())))
	makeCmd.Dir = buildDir
	makeCmd.Env = libopustooling.ScalarDNNBuildEnv()
	if output, err := makeCmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build dred libopus: %w (%s)", err, bytes.TrimSpace(output))
	}
	if err := libopustooling.WriteScalarDNNBuildStamp(buildDir); err != nil {
		return "", "", fmt.Errorf("write dred scalar build stamp: %w", err)
	}

	return sourceDir, buildDir, nil
}

func BuildDREDHelper(repoRoot, sourceFile, outputBase string, includeInternal bool) (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}
	sourceDir, buildDir, err := EnsureDREDBuild(repoRoot)
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

func ProbeFloatQuant(repoRoot string, mode uint32, samples []float32) ([]int16, error) {
	floatQuantHelperOnce.Do(func() {
		floatQuantHelperPath, floatQuantHelperErr = BuildDREDHelper(repoRoot, "libopus_float_quant_info.c", "gopus_shared_float_quant", true)
	})
	if floatQuantHelperErr != nil {
		return nil, floatQuantHelperErr
	}

	var payload bytes.Buffer
	payload.WriteString("GFQI")
	for _, v := range []uint32{1, mode, uint32(len(samples))} {
		if err := binary.Write(&payload, binary.LittleEndian, v); err != nil {
			return nil, err
		}
	}
	for _, sample := range samples {
		if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(sample)); err != nil {
			return nil, err
		}
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(floatQuantHelperPath)
	cmd.Stdin = &payload
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run float quant helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	data := stdout.Bytes()
	if len(data) < 12 || string(data[:4]) != "GFQO" {
		return nil, fmt.Errorf("unexpected float quant helper output")
	}
	count := int(binary.LittleEndian.Uint32(data[8:12]))
	if count != len(samples) {
		return nil, fmt.Errorf("helper count=%d want %d", count, len(samples))
	}
	if len(data) != 12+2*count {
		return nil, fmt.Errorf("helper output length=%d want %d", len(data), 12+2*count)
	}
	out := make([]int16, count)
	offset := 12
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(data[offset:]))
		offset += 2
	}
	return out, nil
}
