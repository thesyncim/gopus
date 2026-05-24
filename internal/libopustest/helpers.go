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
	FloatQuantModeFloat2Int16        = uint32(0)
	FloatQuantModeOSCEOutputScale    = uint32(1)
	FloatQuantModeFARGANSynthInt     = uint32(2)
	FloatQuantModeCELTRaw32767Round  = uint32(3)
	FloatQuantModeCELTDispatch       = uint32(4)
	FloatQuantModeSILKFloat2Short    = uint32(5)
	FloatQuantModeSILKFloat2IntScale = uint32(6)
	FloatQuantModeSILKShort2Float    = uint32(7)
)

var (
	floatQuantHelperOnce sync.Once
	floatQuantHelperPath string
	floatQuantHelperErr  error
)

type scalarDNNBuildConfig struct {
	label          string
	buildFlavor    string
	configureExtra []string
	buildCurrent   func(string) bool
	resetBuild     func(string) error
	buildEnv       func() ([]string, error)
	writeStamp     func(string) error
}

var (
	dredScalarDNNBuild = scalarDNNBuildConfig{
		label:        "dred",
		buildFlavor:  "dred",
		buildCurrent: libopustooling.ScalarDNNBuildIsCurrent,
		resetBuild:   libopustooling.ResetScalarDNNBuildIfStale,
		buildEnv:     libopustooling.ScalarDNNBuildEnv,
		writeStamp:   libopustooling.WriteScalarDNNBuildStamp,
	}
	osceScalarDNNBuild = scalarDNNBuildConfig{
		label:          "osce",
		buildFlavor:    "osce",
		configureExtra: []string{"--enable-osce", "--enable-osce-bwe"},
		buildCurrent:   libopustooling.OSCEScalarDNNBuildIsCurrent,
		resetBuild:     libopustooling.ResetOSCEScalarDNNBuildIfStale,
		buildEnv:       libopustooling.OSCEScalarDNNBuildEnv,
		writeStamp:     libopustooling.WriteOSCEScalarDNNBuildStamp,
	}
)

func EnsureDREDBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	return ensureScalarDNNBuild(repoRoot, dredScalarDNNBuild)
}

func EnsureOSCEBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	return ensureScalarDNNBuild(repoRoot, osceScalarDNNBuild)
}

func ensureScalarDNNBuild(repoRoot string, cfg scalarDNNBuildConfig) (sourceDir, buildDir string, err error) {
	referenceDir := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion)
	sourceDir = filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+"-dredsrc-clean")
	buildDir = filepath.Join(repoRoot, "tmp_check", fmt.Sprintf("build-opus-%s-scalar-%s-%s", cfg.buildFlavor, runtime.GOOS, runtime.GOARCH))
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err == nil && cfg.buildCurrent(buildDir) {
		return sourceDir, buildDir, nil
	}

	if _, err := os.Stat(filepath.Join(sourceDir, "configure")); err != nil {
		libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
		tarball := filepath.Join(repoRoot, "tmp_check", "opus-"+libopustooling.DefaultVersion+".tar.gz")
		if _, err := os.Stat(tarball); err == nil {
			if err := os.RemoveAll(sourceDir); err != nil {
				return "", "", fmt.Errorf("remove stale %s source dir: %w", cfg.label, err)
			}
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				return "", "", fmt.Errorf("mkdir %s source dir: %w", cfg.label, err)
			}
			cmd := exec.Command("tar", "-xzf", tarball, "-C", sourceDir, "--strip-components=1")
			if output, err := cmd.CombinedOutput(); err != nil {
				return "", "", fmt.Errorf("extract %s libopus source: %w (%s)", cfg.label, err, bytes.TrimSpace(output))
			}
		} else if _, refErr := os.Stat(filepath.Join(referenceDir, "configure")); refErr == nil {
			if _, cfgErr := os.Stat(filepath.Join(referenceDir, "Makefile")); cfgErr == nil {
				return "", "", fmt.Errorf("clean %s source tree unavailable: %s is already configured", cfg.label, referenceDir)
			}
			sourceDir = referenceDir
		} else {
			return "", "", fmt.Errorf("libopus tarball not found and no prepared source tree present: %w", err)
		}
	}

	if err := cfg.resetBuild(buildDir); err != nil {
		return "", "", fmt.Errorf("reset stale %s scalar build dir: %w", cfg.label, err)
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir %s build dir: %w", cfg.label, err)
	}
	buildEnv, err := cfg.buildEnv()
	if err != nil {
		return "", "", fmt.Errorf("prepare %s scalar build env: %w", cfg.label, err)
	}

	if _, err := os.Stat(filepath.Join(buildDir, "Makefile")); err != nil {
		configureArgs := []string{
			"--enable-static",
			"--disable-shared",
			"--disable-extra-programs",
			"--enable-dred",
		}
		configureArgs = append(configureArgs, cfg.configureExtra...)
		configureArgs = append(configureArgs,
			"--disable-asm",
			"--disable-rtcd",
			"--disable-intrinsics",
		)
		cmd := exec.Command(filepath.Join(sourceDir, "configure"), configureArgs...)
		cmd.Dir = buildDir
		cmd.Env = buildEnv
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("configure %s libopus build: %w (%s)", cfg.label, err, bytes.TrimSpace(output))
		}
	}

	makeCmd := exec.Command("make", fmt.Sprintf("-j%d", max(1, runtime.NumCPU())))
	makeCmd.Dir = buildDir
	makeCmd.Env = buildEnv
	if output, err := makeCmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build %s libopus: %w (%s)", cfg.label, err, bytes.TrimSpace(output))
	}
	if err := cfg.writeStamp(buildDir); err != nil {
		return "", "", fmt.Errorf("write %s scalar build stamp: %w", cfg.label, err)
	}

	return sourceDir, buildDir, nil
}

type scalarDNNHelperConfig struct {
	label  string
	ensure func(repoRoot string) (sourceDir, buildDir string, err error)
}

func BuildDREDHelper(repoRoot, sourceFile, outputBase string, includeInternal bool) (string, error) {
	return buildScalarDNNHelper(repoRoot, sourceFile, outputBase, includeInternal, scalarDNNHelperConfig{
		label:  "dred",
		ensure: EnsureDREDBuild,
	})
}

func BuildOSCEHelper(repoRoot, sourceFile, outputBase string, includeInternal bool) (string, error) {
	return buildScalarDNNHelper(repoRoot, sourceFile, outputBase, includeInternal, scalarDNNHelperConfig{
		label:  "osce",
		ensure: EnsureOSCEBuild,
	})
}

func buildScalarDNNHelper(repoRoot, sourceFile, outputBase string, includeInternal bool, cfg scalarDNNHelperConfig) (string, error) {
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}
	sourceDir, buildDir, err := cfg.ensure(repoRoot)
	if err != nil {
		return "", err
	}

	srcPath := filepath.Join(repoRoot, "tools", "csrc", sourceFile)
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("%s helper source not found: %w", cfg.label, err)
	}
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err != nil {
		return "", fmt.Errorf("%s libopus static library not found: %w", cfg.label, err)
	}

	outPath := helperOutputPath(buildDir, outputBase, sourceFile, cfg.label)
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
		return "", fmt.Errorf("build %s helper %s: %w (%s)", cfg.label, sourceFile, err, bytes.TrimSpace(output))
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
	helperPath, err := floatQuantHelper()
	if err != nil {
		return nil, err
	}

	payload := NewOraclePayload("GFQI", mode, uint32(len(samples)))
	for _, sample := range samples {
		payload.Float32(sample)
	}

	reader, err := RunOracle(helperPath, payload.Bytes(), "float quant", "GFQO")
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

func ProbeFloatQuantScaledInt32(scale float32, samples []float32) ([]int32, error) {
	helperPath, err := floatQuantHelper()
	if err != nil {
		return nil, err
	}

	payload := NewOraclePayload("GFQI", FloatQuantModeSILKFloat2IntScale, uint32(len(samples)))
	payload.Float32(scale)
	for _, sample := range samples {
		payload.Float32(sample)
	}

	reader, err := RunOracle(helperPath, payload.Bytes(), "float quant", "GFQO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(samples))
	reader.ExpectRemaining(4 * count)
	out := make([]int32, count)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func ProbeSILKShort2Float(samples []int16) ([]float32, error) {
	helperPath, err := floatQuantHelper()
	if err != nil {
		return nil, err
	}

	payload := NewOraclePayload("GFQI", FloatQuantModeSILKShort2Float, uint32(len(samples)))
	for _, sample := range samples {
		payload.I16(sample)
	}

	reader, err := RunOracle(helperPath, payload.Bytes(), "float quant", "GFQO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(samples))
	reader.ExpectRemaining(4 * count)
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func floatQuantHelper() (string, error) {
	floatQuantHelperOnce.Do(func() {
		root := repoRoot()
		libopusStatic := RefPath(".libs", "libopus.a")
		if _, err := os.Stat(libopusStatic); err != nil {
			libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{root})
		}
		floatQuantHelperPath, floatQuantHelperErr = BuildCHelper(CHelperConfig{
			Label:       "float quant",
			OutputBase:  "gopus_shared_float_quant",
			SourceFile:  "libopus_float_quant_info.c",
			RefIncludes: []string{"celt", "silk", "silk/float"},
			Libs:        []string{libopusStatic, "-lm"},
		})
	})
	if floatQuantHelperErr != nil {
		return "", floatQuantHelperErr
	}
	return floatQuantHelperPath, nil
}
