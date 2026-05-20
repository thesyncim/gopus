package libopustest

import (
	"bytes"
	"fmt"
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

func EnsureOSCEBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	referenceDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
	sourceDir = filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+"-dredsrc-clean")
	buildDir = filepath.Join(repoRoot, "tmp_check", fmt.Sprintf("build-opus-osce-scalar-%s-%s", runtime.GOOS, runtime.GOARCH))
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err == nil && libopustooling.OSCEScalarDNNBuildIsCurrent(buildDir) {
		return sourceDir, buildDir, nil
	}

	if _, err := os.Stat(filepath.Join(sourceDir, "configure")); err != nil {
		libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
		tarball := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+".tar.gz")
		if _, err := os.Stat(tarball); err == nil {
			if err := os.RemoveAll(sourceDir); err != nil {
				return "", "", fmt.Errorf("remove stale osce source dir: %w", err)
			}
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				return "", "", fmt.Errorf("mkdir osce source dir: %w", err)
			}
			cmd := exec.Command("tar", "-xzf", tarball, "-C", sourceDir, "--strip-components=1")
			if output, err := cmd.CombinedOutput(); err != nil {
				return "", "", fmt.Errorf("extract osce libopus source: %w (%s)", err, bytes.TrimSpace(output))
			}
		} else if _, refErr := os.Stat(filepath.Join(referenceDir, "configure")); refErr == nil {
			if _, cfgErr := os.Stat(filepath.Join(referenceDir, "Makefile")); cfgErr == nil {
				return "", "", fmt.Errorf("clean osce source tree unavailable: %s is already configured", referenceDir)
			}
			sourceDir = referenceDir
		} else {
			return "", "", fmt.Errorf("libopus tarball not found and no prepared source tree present: %w", err)
		}
	}

	if err := libopustooling.ResetOSCEScalarDNNBuildIfStale(buildDir); err != nil {
		return "", "", fmt.Errorf("reset stale osce scalar build dir: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir osce build dir: %w", err)
	}

	if _, err := os.Stat(filepath.Join(buildDir, "Makefile")); err != nil {
		cmd := exec.Command(filepath.Join(sourceDir, "configure"),
			"--enable-static",
			"--disable-shared",
			"--disable-extra-programs",
			"--enable-dred",
			"--enable-osce",
			"--enable-osce-bwe",
			"--disable-asm",
			"--disable-rtcd",
			"--disable-intrinsics",
		)
		cmd.Dir = buildDir
		cmd.Env = libopustooling.OSCEScalarDNNBuildEnv()
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("configure osce libopus build: %w (%s)", err, bytes.TrimSpace(output))
		}
	}

	makeCmd := exec.Command("make", fmt.Sprintf("-j%d", max(1, runtime.NumCPU())))
	makeCmd.Dir = buildDir
	makeCmd.Env = libopustooling.OSCEScalarDNNBuildEnv()
	if output, err := makeCmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build osce libopus: %w (%s)", err, bytes.TrimSpace(output))
	}
	if err := libopustooling.WriteOSCEScalarDNNBuildStamp(buildDir); err != nil {
		return "", "", fmt.Errorf("write osce scalar build stamp: %w", err)
	}

	return sourceDir, buildDir, nil
}

func BuildOSCEHelper(repoRoot, sourceFile, outputBase string, includeInternal bool) (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}
	sourceDir, buildDir, err := EnsureOSCEBuild(repoRoot)
	if err != nil {
		return "", err
	}

	srcPath := filepath.Join(repoRoot, "tools", "csrc", sourceFile)
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("osce helper source not found: %w", err)
	}
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err != nil {
		return "", fmt.Errorf("osce libopus static library not found: %w", err)
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
		return "", fmt.Errorf("build osce helper %s: %w (%s)", sourceFile, err, bytes.TrimSpace(output))
	}
	return outPath, nil
}

func RunOracle(binPath string, input []byte, label, outputMagic string) (*OracleReader, error) {
	return RunOracleEnv(binPath, input, label, outputMagic, nil)
}

func RunOracleEnv(binPath string, input []byte, label, outputMagic string, env []string) (*OracleReader, error) {
	return RunOracleVersionEnv(binPath, input, label, outputMagic, 1, env)
}

func RunOracleVersion(binPath string, input []byte, label, outputMagic string, wantVersion uint32) (*OracleReader, error) {
	return RunOracleVersionEnv(binPath, input, label, outputMagic, wantVersion, nil)
}

func RunOracleVersionEnv(binPath string, input []byte, label, outputMagic string, wantVersion uint32, env []string) (*OracleReader, error) {
	data, err := RunHelperEnv(binPath, input, env)
	if err != nil {
		return nil, fmt.Errorf("run %s helper: %w", label, err)
	}
	reader, version, err := NewOracleReaderVersion(label, outputMagic, data)
	if err != nil {
		return nil, err
	}
	if version != wantVersion {
		return nil, fmt.Errorf("%s helper version=%d want %d", label, version, wantVersion)
	}
	return reader, nil
}

func ProbeFloatQuant(mode uint32, samples []float32) ([]int16, error) {
	floatQuantHelperOnce.Do(func() {
		floatQuantHelperPath, floatQuantHelperErr = BuildCHelper(CHelperConfig{
			Label:       "float quant",
			OutputBase:  "gopus_shared_float_quant",
			SourceFile:  "libopus_float_quant_info.c",
			RefIncludes: []string{"celt"},
		})
	})
	if floatQuantHelperErr != nil {
		return nil, floatQuantHelperErr
	}

	payload := NewOraclePayload("GFQI", mode, uint32(len(samples)))
	for _, sample := range samples {
		payload.Float32(sample)
	}

	reader, err := RunOracle(floatQuantHelperPath, payload.Bytes(), "float quant", "GFQO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(samples))
	reader.ExpectRemaining(2 * count)
	out := make([]int16, count)
	for i := range out {
		out[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
