package libopustest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
	buildEnv       func() ([]string, error)
	writeStamp     func(string) error
	simd           bool
}

var (
	dredScalarDNNBuild = scalarDNNBuildConfig{
		label:        "dred",
		buildFlavor:  "dred",
		buildCurrent: libopustooling.ScalarDNNBuildIsCurrent,
		buildEnv:     libopustooling.ScalarDNNBuildEnv,
		writeStamp:   libopustooling.WriteScalarDNNBuildStamp,
	}
	dredSIMDDNNBuild = scalarDNNBuildConfig{
		label:        "dred",
		buildFlavor:  "dred-simd",
		buildCurrent: libopustooling.DREDSIMDBuildIsCurrent,
		buildEnv:     libopustooling.DREDSIMDBuildEnv,
		writeStamp:   libopustooling.WriteDREDSIMDBuildStamp,
		simd:         true,
	}
	osceScalarDNNBuild = scalarDNNBuildConfig{
		label:          "osce",
		buildFlavor:    "osce",
		configureExtra: []string{"--enable-osce", "--enable-osce-bwe"},
		buildCurrent:   libopustooling.OSCEScalarDNNBuildIsCurrent,
		buildEnv:       libopustooling.OSCEScalarDNNBuildEnv,
		writeStamp:     libopustooling.WriteOSCEScalarDNNBuildStamp,
	}
)

func EnsureDREDBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", "", err
	}
	if variant == libopustooling.LibopusReferenceSIMD {
		return ensureScalarDNNBuild(repoRoot, dredSIMDDNNBuild)
	}
	return ensureScalarDNNBuild(repoRoot, dredScalarDNNBuild)
}

func EnsureOSCEBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	return ensureScalarDNNBuild(repoRoot, osceScalarDNNBuild)
}

func ensureScalarDNNBuild(repoRoot string, cfg scalarDNNBuildConfig) (sourceDir, buildDir string, err error) {
	lock, err := lockOracleBuild(filepath.Join(repoRoot, "tmp_check", ".dnn-build.lock"))
	if err != nil {
		return "", "", err
	}
	defer func() { _ = lock.Close() }()

	sourceDir, err = ensureDNNSource(repoRoot)
	if err != nil {
		return "", "", err
	}
	buildDir = filepath.Join(repoRoot, "tmp_check", fmt.Sprintf("build-opus-%s-scalar-%s-%s", cfg.buildFlavor, runtime.GOOS, runtime.GOARCH))
	libopusStatic := filepath.Join(buildDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err == nil && cfg.buildCurrent(buildDir) {
		if err := validateDREDInstructionBuild(buildDir, cfg); err != nil {
			return "", "", err
		}
		return sourceDir, buildDir, nil
	}

	// Configure and make can outlive a killed Go test process. Their private
	// directory cannot be reset by the next lock owner; only a completed build
	// becomes visible at the stable archive path.
	publishedBuildDir := buildDir
	buildDir, err = os.MkdirTemp(filepath.Dir(buildDir), filepath.Base(buildDir)+".stage-")
	if err != nil {
		return "", "", fmt.Errorf("stage %s build: %w", cfg.label, err)
	}
	stagingBuildDir := buildDir
	defer func() { _ = os.RemoveAll(stagingBuildDir) }()
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
		if cfg.simd {
			configureArgs = append(configureArgs, "--enable-rtcd", "--enable-intrinsics")
		} else {
			configureArgs = append(configureArgs,
				"--disable-asm", "--disable-rtcd", "--disable-intrinsics")
		}
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
	if err := validateDREDInstructionBuild(buildDir, cfg); err != nil {
		return "", "", err
	}

	if err := os.RemoveAll(publishedBuildDir); err != nil {
		return "", "", fmt.Errorf("replace stale %s build: %w", cfg.label, err)
	}
	if err := os.Rename(buildDir, publishedBuildDir); err != nil {
		return "", "", fmt.Errorf("publish %s build: %w", cfg.label, err)
	}
	return sourceDir, publishedBuildDir, nil
}

// ensureDNNSource publishes an entire unconfigured source tree atomically.
// The caller holds the common DRED/OSCE build lock. An interrupted extraction
// leaves only a private staging directory, never a partially visible source.
func ensureDNNSource(repoRoot string) (string, error) {
	tmpDir := filepath.Join(repoRoot, "tmp_check")
	sourceDir := filepath.Join(tmpDir, "opus-"+libopustooling.DefaultVersion+"-dnnsrc-atomic")
	if _, err := os.Stat(sourceDir); err == nil {
		if err := validateDNNSource(sourceDir); err != nil {
			return "", err
		}
		return sourceDir, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tarball := filepath.Join(tmpDir, "opus-"+libopustooling.DefaultVersion+".tar.gz")
	if _, err := os.Stat(tarball); os.IsNotExist(err) {
		libopustooling.EnsureLibopus(libopustooling.DefaultVersion, []string{repoRoot})
	}
	if _, err := os.Stat(tarball); err != nil {
		return "", fmt.Errorf("pinned DNN libopus source archive unavailable: %w", err)
	}
	staging, err := os.MkdirTemp(tmpDir, ".opus-dnn-source-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	// Run tar beside the archive with relative names: GNU tar reads a
	// drive-letter archive path such as D:\... as a remote host:path.
	cmd := exec.Command("tar", "-xzf", filepath.Base(tarball), "-C", filepath.Base(staging), "--strip-components=1")
	cmd.Dir = tmpDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("extract DNN libopus source: %w (%s)", err, bytes.TrimSpace(output))
	}
	if err := validateDNNSource(staging); err != nil {
		return "", err
	}
	if err := os.Rename(staging, sourceDir); err != nil {
		return "", fmt.Errorf("publish DNN source: %w", err)
	}
	return sourceDir, nil
}

func validateDNNSource(sourceDir string) error {
	for _, name := range []string{"configure", "install-sh", "config.sub", "include/opus.h", "dnn/nnet.c"} {
		if _, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(name))); err != nil {
			return fmt.Errorf("DNN source is missing %s: %w", name, err)
		}
	}
	return nil
}

func validateDREDInstructionBuild(buildDir string, cfg scalarDNNBuildConfig) error {
	if cfg.buildFlavor != "dred" && cfg.buildFlavor != "dred-simd" {
		return nil
	}
	config, err := os.ReadFile(filepath.Join(buildDir, "config.h"))
	if err != nil {
		return fmt.Errorf("read DRED %s config: %w", cfg.buildFlavor, err)
	}
	defined := func(name string) bool {
		for _, line := range strings.Split(string(config), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "#define" && fields[1] == name {
				return true
			}
		}
		return false
	}
	if !defined("ENABLE_DRED") || !defined("ENABLE_DEEP_PLC") || defined("FIXED_POINT") ||
		defined("ENABLE_OSCE") || defined("ENABLE_QEXT") || defined("CUSTOM_MODES") {
		return fmt.Errorf("DRED %s reference has mismatched optional features", cfg.buildFlavor)
	}
	variant := libopustooling.LibopusReferenceScalar
	if cfg.simd {
		variant = libopustooling.LibopusReferenceSIMD
	}
	if err := libopustooling.ValidateLibopusInstructionConfig(string(config), variant, runtime.GOARCH); err != nil {
		return fmt.Errorf("DRED %s instruction config: %w", cfg.buildFlavor, err)
	}
	return nil
}

type scalarDNNHelperConfig struct {
	label  string
	ensure func(repoRoot string) (sourceDir, buildDir string, err error)
	cflags string
}

func BuildDREDHelper(repoRoot, sourceFile, outputBase string, includeInternal bool) (string, error) {
	if dredQEXTReferenceEnabled {
		includes := []string{"dnn"}
		if includeInternal {
			includes = append(includes, "celt", "silk", "src")
		}
		return BuildCHelper(CHelperConfig{
			Label:       "combined DRED-QEXT " + outputBase,
			OutputBase:  outputBase,
			SourceFile:  sourceFile,
			CFlags:      []string{"-DHAVE_CONFIG_H"},
			RefIncludes: includes,
			DREDQEXTRef: true,
			Libs:        []string{DREDQEXTRefPath(".libs", "libopus.a"), "-lm"},
		})
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	cflags := libopustooling.ScalarDNNBuildCFLAGS
	if variant == libopustooling.LibopusReferenceSIMD {
		cflags = libopustooling.DREDSIMDBuildCFLAGS
	}
	return buildScalarDNNHelper(repoRoot, sourceFile, outputBase, includeInternal, scalarDNNHelperConfig{
		label:  "dred",
		ensure: EnsureDREDBuild,
		cflags: cflags,
	})
}

func BuildOSCEHelper(repoRoot, sourceFile, outputBase string, includeInternal bool) (string, error) {
	return buildScalarDNNHelper(repoRoot, sourceFile, outputBase, includeInternal, scalarDNNHelperConfig{
		label:  "osce",
		ensure: EnsureOSCEBuild,
		cflags: "-O2",
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

	args := append([]string{"-std=c99"}, strings.Fields(cfg.cflags)...)
	args = append(args,
		"-DHAVE_CONFIG_H",
		"-I", buildDir,
		"-I", filepath.Join(sourceDir, "include"),
	)
	if includeInternal {
		args = append(args,
			"-I", sourceDir,
			"-I", filepath.Join(sourceDir, "src"),
			"-I", filepath.Join(sourceDir, "celt"),
			"-I", filepath.Join(sourceDir, "dnn"),
			"-I", filepath.Join(sourceDir, "silk"),
		)
	}
	args = append(args, srcPath, libopusStatic, "-lm")
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("read %s helper source: %w", cfg.label, err)
	}
	archiveBytes, err := os.ReadFile(libopusStatic)
	if err != nil {
		return "", fmt.Errorf("read %s archive: %w", cfg.label, err)
	}
	configBytes, err := os.ReadFile(filepath.Join(buildDir, "config.h"))
	if err != nil {
		return "", fmt.Errorf("read %s config: %w", cfg.label, err)
	}
	version, _ := exec.Command(ccPath, "--version").Output()
	hash := sha256.New()
	for _, part := range [][]byte{[]byte(ccPath), version, []byte(strings.Join(args, "\x00")), srcBytes, archiveBytes, configBytes} {
		_, _ = hash.Write(part)
	}
	digest := hex.EncodeToString(hash.Sum(nil))[:16]
	outPath := helperOutputPathWithDigest(buildDir, outputBase, sourceFile, cfg.label, digest)
	if _, err := os.Stat(outPath); err == nil {
		return outPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	tmpPattern := "." + outputBase + "-*.tmp"
	if runtime.GOOS == "windows" {
		tmpPattern += ".exe"
	}
	tmp, err := os.CreateTemp(buildDir, tmpPattern)
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()
	args = append(args, "-o", tmpPath)

	cmd := exec.Command(ccPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s helper %s: %w (%s)", cfg.label, sourceFile, err, bytes.TrimSpace(output))
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		if _, statErr := os.Stat(outPath); statErr == nil {
			return outPath, nil
		}
		return "", fmt.Errorf("install %s helper %s: %w", cfg.label, sourceFile, err)
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
		libopusStatic := RefPath(".libs", "libopus.a")
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
