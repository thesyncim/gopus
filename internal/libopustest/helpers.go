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

	"github.com/thesyncim/gopus/internal/extsupport"
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
	FloatQuantModeFloat2Int24        = uint32(8)
)

var (
	floatQuantHelperOnce sync.Once
	floatQuantHelperPath string
	floatQuantHelperErr  error
)

type scalarDNNBuildConfig struct {
	label        string
	buildFlavor  string
	cflags       string
	dred         bool
	osce         bool
	qext         bool
	custom       bool
	buildCurrent func(string) bool
	buildEnv     func() ([]string, error)
	writeStamp   func(string) error
	simd         bool
}

var (
	dredScalarDNNBuild = scalarDNNBuildConfig{
		label:        "dred",
		buildFlavor:  "dred",
		dred:         true,
		buildCurrent: libopustooling.ScalarDNNBuildIsCurrent,
		buildEnv:     libopustooling.ScalarDNNBuildEnv,
		writeStamp:   libopustooling.WriteScalarDNNBuildStamp,
	}
	dredSIMDDNNBuild = scalarDNNBuildConfig{
		label:        "dred",
		buildFlavor:  "dred-simd",
		dred:         true,
		buildCurrent: libopustooling.DREDSIMDBuildIsCurrent,
		buildEnv:     libopustooling.DREDSIMDBuildEnv,
		writeStamp:   libopustooling.WriteDREDSIMDBuildStamp,
		simd:         true,
	}
)

func EnsureDREDBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	if err := validateDNNFloatReferencePairing(true); err != nil {
		return "", "", err
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", "", err
	}
	if osceDNNFeatureEnabled || extsupport.QEXT || customModesReferenceEnabled {
		return ensureScalarDNNBuild(repoRoot, featureDNNBuildConfig(true, osceDNNFeatureEnabled, extsupport.QEXT, customModesReferenceEnabled, variant))
	}
	if variant == libopustooling.LibopusReferenceSIMD {
		return ensureScalarDNNBuild(repoRoot, dredSIMDDNNBuild)
	}
	return ensureScalarDNNBuild(repoRoot, dredScalarDNNBuild)
}

func EnsureOSCEBuild(repoRoot string) (sourceDir, buildDir string, err error) {
	if err := validateDNNFloatReferencePairing(extsupport.DRED); err != nil {
		return "", "", err
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", "", err
	}
	return ensureScalarDNNBuild(repoRoot, featureDNNBuildConfig(extsupport.DRED, true, extsupport.QEXT, customModesReferenceEnabled, variant))
}

func validateDNNFloatReferencePairing(dred bool) error {
	if !decodeSequenceFixedRef {
		return nil
	}
	feature := "ENABLE_OSCE"
	if dred {
		feature = "ENABLE_DRED"
	}
	return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf(
		"pinned libopus %s rejects FIXED_POINT with %s; no matching DNN reference archive exists",
		libopustooling.DefaultVersion, feature,
	)}
}

const featureDNNBuildStampFile = ".gopus-dnn-feature-build"

const osceScalarNoVectorCFLAGS = libopustooling.OSCEScalarDNNBuildCFLAGS + " " + libopustooling.LibopusScalarCVectorizationFlags

func osceScalarNoVectorBuildEnv() ([]string, error) {
	env, err := libopustooling.OSCEScalarDNNBuildEnv()
	if err != nil {
		return nil, err
	}
	for i, value := range env {
		if strings.HasPrefix(value, "CFLAGS=") {
			env[i] = "CFLAGS=" + osceScalarNoVectorCFLAGS
			return env, nil
		}
	}
	return nil, fmt.Errorf("OSCE build environment is missing CFLAGS")
}

// featureDNNBuildConfig gives every optional-feature combination its own
// archive and compiler contract. DRED-only uses its established build paths.
func featureDNNBuildConfig(dred, osce, qext, custom bool, variant libopustooling.LibopusReferenceVariant) scalarDNNBuildConfig {
	return featureDNNBuildConfigWithExtraCFlags(dred, osce, qext, custom, variant, "")
}

func featureDNNBuildConfigWithExtraCFlags(dred, osce, qext, custom bool, variant libopustooling.LibopusReferenceVariant, extraCFlags string) scalarDNNBuildConfig {
	features := make([]string, 0, 4)
	if dred {
		features = append(features, "dred")
	}
	if osce {
		features = append(features, "osce")
	}
	if qext {
		features = append(features, "qext")
	}
	if custom {
		features = append(features, "custom")
	}
	flavor := "dnn-" + strings.Join(features, "-")
	simd := variant == libopustooling.LibopusReferenceSIMD
	if simd {
		flavor += "-simd"
	}
	if extraCFlags != "" {
		flavor += "-weights-file"
	}
	cflags := osceScalarNoVectorCFLAGS
	buildEnv := osceScalarNoVectorBuildEnv
	if simd {
		cflags = libopustooling.DREDSIMDBuildCFLAGS
		buildEnv = libopustooling.DREDSIMDBuildEnv
	} else if !osce {
		cflags = libopustooling.ScalarDNNBuildCFLAGS
		buildEnv = libopustooling.ScalarDNNBuildEnv
	}
	if extraCFlags != "" {
		cflags += " " + extraCFlags
		baseBuildEnv := buildEnv
		buildEnv = func() ([]string, error) {
			env, err := baseBuildEnv()
			if err != nil {
				return nil, err
			}
			for i, value := range env {
				if strings.HasPrefix(value, "CFLAGS=") {
					env[i] = "CFLAGS=" + strings.TrimPrefix(value, "CFLAGS=") + " " + extraCFlags
					return env, nil
				}
			}
			return nil, fmt.Errorf("%s build environment is missing CFLAGS", strings.Join(features, "+"))
		}
	}
	cfg := scalarDNNBuildConfig{
		label:       strings.Join(features, "+"),
		buildFlavor: flavor,
		cflags:      cflags,
		dred:        dred,
		osce:        osce,
		qext:        qext,
		custom:      custom,
		buildEnv:    buildEnv,
		simd:        simd,
	}
	cfg.buildCurrent = func(buildDir string) bool {
		data, err := os.ReadFile(filepath.Join(buildDir, featureDNNBuildStampFile))
		if err != nil {
			return false
		}
		stamp, err := featureDNNBuildStamp(cfg, cflags)
		return err == nil && string(data) == stamp
	}
	cfg.writeStamp = func(buildDir string) error {
		stamp, err := featureDNNBuildStamp(cfg, cflags)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(buildDir, featureDNNBuildStampFile), []byte(stamp), 0o644)
	}
	return cfg
}

func featureDNNBuildStamp(cfg scalarDNNBuildConfig, cflags string) (string, error) {
	cc, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", err
	}
	target, err := compilerFirstLine(cc, "-dumpmachine")
	if err != nil {
		return "", err
	}
	if err := validateDNNCompilerTarget(target, runtime.GOARCH); err != nil {
		return "", err
	}
	version, err := compilerFirstLine(cc, "--version")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("gopus libopus DNN feature build v2\n")
	for _, item := range []string{
		"LIBOPUS_VERSION=" + libopustooling.DefaultVersion,
		"GOOS=" + runtime.GOOS,
		"GOARCH=" + runtime.GOARCH,
		"CC=" + cc,
		"CC_TARGET=" + target,
		"CC_VERSION=" + version,
		"CFLAGS=" + cflags,
		"CPPFLAGS=",
		"LDFLAGS=",
		"CONFIGURE=" + strings.Join(dnnConfigureArgs(cfg), " "),
		fmt.Sprintf("CUSTOM_MODES=%t", cfg.custom),
	} {
		b.WriteString(item + "\n")
	}
	if cfg.dred {
		b.WriteString("DRED_MODEL_SOURCES=" + libopustooling.DREDModelSourcesStamp() + "\n")
	}
	return b.String(), nil
}

func validateDNNCompilerTarget(target, goarch string) error {
	arch, _, _ := strings.Cut(strings.ToLower(target), "-")
	paired := false
	switch goarch {
	case "amd64":
		paired = arch == "x86_64" || arch == "amd64"
	case "arm64":
		paired = arch == "aarch64" || arch == "arm64"
	case "386":
		paired = arch == "i386" || arch == "i486" || arch == "i586" || arch == "i686"
	case "arm":
		paired = strings.HasPrefix(arch, "arm")
	default:
		paired = arch == goarch
	}
	if !paired {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("DNN C compiler target %q does not match GOARCH=%s", target, goarch)}
	}
	return nil
}

func compilerFirstLine(cc, arg string) (string, error) {
	output, err := exec.Command(cc, arg).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("identify C compiler %s %s: %w (%s)", cc, arg, err, bytes.TrimSpace(output))
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	if line == "" {
		return "", fmt.Errorf("identify C compiler %s %s: empty output", cc, arg)
	}
	return line, nil
}

func dnnConfigureArgs(cfg scalarDNNBuildConfig) []string {
	args := []string{"--enable-static", "--disable-shared", "--disable-extra-programs"}
	if cfg.dred {
		args = append(args, "--enable-dred")
	}
	if cfg.osce {
		args = append(args, "--enable-osce", "--enable-osce-bwe")
	}
	if cfg.qext {
		args = append(args, "--enable-qext")
	}
	if cfg.custom {
		args = append(args, "--enable-custom-modes")
	}
	if cfg.simd {
		return append(args, "--enable-rtcd", "--enable-intrinsics")
	}
	return append(args, "--disable-asm", "--disable-rtcd", "--disable-intrinsics")
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
	if cfg.dred {
		if err := libopustooling.ValidateDREDModelSources(sourceDir); err != nil {
			return "", "", fmt.Errorf("validate pinned DNN models for %s build: %w", cfg.label, err)
		}
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
		cmd := exec.Command(filepath.Join(sourceDir, "configure"), dnnConfigureArgs(cfg)...)
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
	for _, name := range []string{"configure", "install-sh", "config.sub", "include/opus.h", "dnn/nnet.c", "package_version"} {
		if _, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(name))); err != nil {
			return fmt.Errorf("DNN source is missing %s: %w", name, err)
		}
	}
	version, err := os.ReadFile(filepath.Join(sourceDir, "package_version"))
	if err != nil {
		return fmt.Errorf("read DNN source version: %w", err)
	}
	versionKey, versionValue, found := strings.Cut(strings.TrimSpace(string(version)), "=")
	if !found || strings.TrimSpace(versionKey) != "PACKAGE_VERSION" || strings.Trim(strings.TrimSpace(versionValue), `"'`) != libopustooling.DefaultVersion {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf(
			"DNN source version does not match pinned libopus %s", libopustooling.DefaultVersion,
		)}
	}
	return nil
}

func validateDREDInstructionBuild(buildDir string, cfg scalarDNNBuildConfig) error {
	config, err := os.ReadFile(filepath.Join(buildDir, "config.h"))
	if err != nil {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("read DNN %s config: %w", cfg.buildFlavor, err)}
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
	enabled := func(name string) bool {
		for _, line := range strings.Split(string(config), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[0] == "#define" && fields[1] == name && fields[2] == "1" {
				return true
			}
		}
		return false
	}
	featureMatches := func(name string, want bool) bool {
		if want {
			return enabled(name)
		}
		return !defined(name)
	}
	if !featureMatches("ENABLE_DRED", cfg.dred) || !featureMatches("ENABLE_OSCE", cfg.osce) ||
		!featureMatches("ENABLE_OSCE_BWE", cfg.osce) || !featureMatches("ENABLE_QEXT", cfg.qext) ||
		!enabled("ENABLE_DEEP_PLC") || defined("ENABLE_OSCE_TRAINING_DATA") ||
		defined("FIXED_POINT") || !featureMatches("CUSTOM_MODES", cfg.custom) {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("DNN %s reference has mismatched optional features", cfg.buildFlavor)}
	}
	variant := libopustooling.LibopusReferenceScalar
	if cfg.simd {
		variant = libopustooling.LibopusReferenceSIMD
	}
	if err := libopustooling.ValidateLibopusInstructionConfig(string(config), variant, runtime.GOARCH); err != nil {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("DNN %s instruction config: %w", cfg.buildFlavor, err)}
	}
	return nil
}

// dnnHelperIncludeArgs keeps generated config.h ahead of the source tree while
// allowing helpers to include libopus headers from its repository root.
func dnnHelperIncludeArgs(buildDir, sourceDir string) []string {
	return []string{
		"-DHAVE_CONFIG_H",
		"-I", buildDir,
		"-I", sourceDir,
		"-I", filepath.Join(sourceDir, "include"),
		"-I", filepath.Join(sourceDir, "dnn"),
	}
}

type scalarDNNHelperConfig struct {
	label  string
	ensure func(repoRoot string) (sourceDir, buildDir string, err error)
	cflags string
}

func BuildDREDHelper(root, sourceFile, outputBase string, includeInternal bool) (string, error) {
	if err := validateDREDReferenceBuildPairing(); err != nil {
		return "", err
	}
	if err := validateDNNFloatReferencePairing(true); err != nil {
		return "", err
	}
	if dredQEXTReferenceEnabled && !customModesReferenceEnabled {
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
	if root == "" {
		root = repoRoot()
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	cflags := libopustooling.ScalarDNNBuildCFLAGS
	if osceDNNFeatureEnabled {
		cflags = osceScalarNoVectorCFLAGS
	}
	if variant == libopustooling.LibopusReferenceSIMD {
		cflags = libopustooling.DREDSIMDBuildCFLAGS
	}
	return buildScalarDNNHelper(root, sourceFile, outputBase, includeInternal, scalarDNNHelperConfig{
		label:  "dred",
		ensure: EnsureDREDBuild,
		cflags: cflags,
	})
}

// BuildDREDWeightsFileHelper builds a DNN-feature reference with USE_WEIGHTS_FILE
// defined for both libopus and the helper. It lets sequence oracles load the
// same runtime model blob at the same point as the Go decoder.
func BuildDREDWeightsFileHelper(root, sourceFile, outputBase string, includeInternal bool) (string, error) {
	if err := validateDREDReferenceBuildPairing(); err != nil {
		return "", err
	}
	if err := validateDNNFloatReferencePairing(true); err != nil {
		return "", err
	}
	if root == "" {
		root = repoRoot()
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	cfg := featureDNNBuildConfigWithExtraCFlags(
		true, osceDNNFeatureEnabled, extsupport.QEXT, customModesReferenceEnabled,
		variant, "-DUSE_WEIGHTS_FILE",
	)
	return buildScalarDNNHelper(root, sourceFile, outputBase, includeInternal, scalarDNNHelperConfig{
		label: "DRED weights-file",
		ensure: func(repoRoot string) (string, string, error) {
			return ensureScalarDNNBuild(repoRoot, cfg)
		},
		cflags: cfg.cflags,
	})
}

func BuildOSCEHelper(root, sourceFile, outputBase string, includeInternal bool) (string, error) {
	if err := validateDNNFloatReferencePairing(extsupport.DRED); err != nil {
		return "", err
	}
	if root == "" {
		root = repoRoot()
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	cflags := osceScalarNoVectorCFLAGS
	if variant == libopustooling.LibopusReferenceSIMD {
		cflags = libopustooling.DREDSIMDBuildCFLAGS
	}
	return buildScalarDNNHelper(root, sourceFile, outputBase, includeInternal, scalarDNNHelperConfig{
		label:  "osce",
		ensure: EnsureOSCEBuild,
		cflags: cflags,
	})
}

func buildScalarDNNHelper(repoRoot, sourceFile, outputBase string, includeInternal bool, cfg scalarDNNHelperConfig) (string, error) {
	if target, err := libopustooling.ResolveLibopusAMD64Target(); err != nil {
		return "", err
	} else if target != "" {
		return "", &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("%s does not support DNN feature references", libopustooling.LibopusAMD64TargetEnv)}
	}
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

	args := dnnHelperCompileFlags(cfg.cflags)
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

// BuildDNNCHelper builds a public C oracle against the optional DNN features
// and instruction lane selected by the current Go build. Its caller supplies
// the usual helper source options but no libopus archive or reference selector.
func BuildDNNCHelper(root string, cfg CHelperConfig) (string, error) {
	if target, err := libopustooling.ResolveLibopusAMD64Target(); err != nil {
		return "", err
	} else if target != "" {
		return "", &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("%s does not support DNN feature references", libopustooling.LibopusAMD64TargetEnv)}
	}
	if !osceDNNFeatureEnabled && !extsupport.DRED {
		return "", &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("no DNN feature is enabled in the Go build")}
	}
	if cfg.OutputBase == "" || cfg.SourceFile == "" {
		return "", fmt.Errorf("helper output base and source file are required")
	}
	linkInputs := make([]string, 0, len(cfg.Libs)+len(cfg.LDFlags)+len(cfg.CFlags))
	linkInputs = append(linkInputs, cfg.Libs...)
	linkInputs = append(linkInputs, cfg.LDFlags...)
	linkInputs = append(linkInputs, cfg.CFlags...)
	if err := validateNoLibopusLibraryOverride(linkInputs); err != nil {
		return "", err
	}
	if cfg.QEXTRef || cfg.FixedRef || cfg.FixedQEXTRef || cfg.DREDQEXTRef || cfg.CustomRef ||
		cfg.CustomQEXTRef || cfg.CustomFixedRef || cfg.CustomFixedQEXTRef || cfg.SIMDRef || cfg.ForceScalarRef {
		return "", &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("DNN helper reference selectors conflict with the current Go feature and instruction lane")}
	}
	if err := validateDNNFloatReferencePairing(extsupport.DRED); err != nil {
		return "", err
	}
	if dredQEXTReferenceEnabled && !customModesReferenceEnabled {
		cfg.DREDQEXTRef = true
		cfg.CFlags = append(cfg.CFlags, "-DHAVE_CONFIG_H")
		cfg.RefIncludes = append(cfg.RefIncludes, "dnn")
		if len(cfg.Libs) == 0 {
			cfg.Libs = []string{"-lm"}
		}
		cfg.Libs = append([]string{DREDQEXTRefPath(".libs", "libopus.a")}, cfg.Libs...)
		return BuildCHelper(cfg)
	}
	if root == "" {
		root = repoRoot()
	}
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}
	ensure := EnsureDREDBuild
	if osceDNNFeatureEnabled {
		ensure = EnsureOSCEBuild
	}
	sourceDir, buildDir, err := ensure(root)
	if err != nil {
		return "", err
	}
	if cfg.ProbeRelPath != "" {
		if _, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(cfg.ProbeRelPath))); err != nil {
			return "", fmt.Errorf("DNN helper source is missing %s: %w", cfg.ProbeRelPath, err)
		}
	}
	srcPath := cfg.SourceFile
	if !filepath.IsAbs(srcPath) {
		srcPath = filepath.Join(root, "tools", "csrc", filepath.FromSlash(srcPath))
	}
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("DNN helper source not found: %w", err)
	}
	archive := filepath.Join(buildDir, ".libs", "libopus.a")
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	cflags := osceScalarNoVectorCFLAGS
	if !osceDNNFeatureEnabled {
		cflags = libopustooling.ScalarDNNBuildCFLAGS
	}
	if variant == libopustooling.LibopusReferenceSIMD {
		cflags = libopustooling.DREDSIMDBuildCFLAGS
	}
	args := dnnHelperCompileFlags(cflags)
	if cfg.DeadStrip {
		args = append(args, "-ffunction-sections", "-fdata-sections")
	}
	args = append(args, cfg.CFlags...)
	args = append(args, dnnHelperIncludeArgs(buildDir, sourceDir)...)
	for _, rel := range cfg.RefIncludes {
		args = append(args, "-I", filepath.Join(sourceDir, filepath.FromSlash(rel)))
	}
	for _, dir := range cfg.IncludeDirs {
		args = append(args, "-I", dir)
	}
	args = append(args, srcPath)
	for _, rel := range cfg.RefSources {
		args = append(args, filepath.Join(sourceDir, filepath.FromSlash(rel)))
	}
	args = append(args, cfg.Sources...)
	args = append(args, archive)
	libs := cfg.Libs
	if len(libs) == 0 {
		libs = []string{"-lm"}
	}
	args = append(args, libs...)
	if cfg.DeadStrip {
		if runtime.GOOS == "darwin" {
			args = append(args, "-Wl,-dead_strip")
		} else {
			args = append(args, "-Wl,--gc-sections")
		}
	}
	args = append(args, cfg.LDFlags...)
	hash := sha256.New()
	version, _ := exec.Command(ccPath, "--version").Output()
	for _, part := range [][]byte{[]byte(ccPath), version, []byte(strings.Join(args, "\x00"))} {
		_, _ = hash.Write(part)
	}
	for _, path := range append([]string{srcPath, archive, filepath.Join(buildDir, "config.h")}, append(cfg.Sources, dnnReferenceSourcePaths(sourceDir, cfg.RefSources)...)...) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read DNN helper input %s: %w", path, err)
		}
		_, _ = hash.Write(data)
	}
	digest := hex.EncodeToString(hash.Sum(nil))[:16]
	outPath := helperOutputPathWithDigest(buildDir, cfg.OutputBase, cfg.SourceFile, "dnn", digest)
	if _, err := os.Stat(outPath); err == nil {
		return outPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	tmpPattern := "." + cfg.OutputBase + "-*.tmp"
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
	cmd := exec.Command(ccPath, append(args, "-o", tmpPath)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build DNN helper %s: %w (%s)", cfg.SourceFile, err, bytes.TrimSpace(output))
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		if _, statErr := os.Stat(outPath); statErr == nil {
			return outPath, nil
		}
		return "", fmt.Errorf("install DNN helper %s: %w", cfg.SourceFile, err)
	}
	return outPath, nil
}

// dnnHelperCompileFlags inherits the paired reference's C dialect and FP
// contraction defaults; cfg.cflags carries the selected archive's flags.
func dnnHelperCompileFlags(cflags string) []string {
	return strings.Fields(cflags)
}

func validateNoLibopusLibraryOverride(linkInputs []string) error {
	for i, input := range linkInputs {
		name := strings.ToLower(strings.TrimSpace(input))
		if isLibopusLinkInput(name) {
			return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("helper must use its selected libopus archive, not %q", input)}
		}
		if (name == "-l" || name == "-framework") && i+1 < len(linkInputs) && isLibopusLibraryName(strings.TrimSpace(linkInputs[i+1])) {
			return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("helper must use its selected libopus archive, not %s %s", input, linkInputs[i+1])}
		}
		if strings.HasPrefix(name, "-wl,") {
			parts := strings.Split(name, ",")
			for j, part := range parts {
				if isLibopusLinkInput(part) || (part == "-l" || part == "-framework") && j+1 < len(parts) && isLibopusLibraryName(parts[j+1]) {
					return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("helper must use its selected libopus archive, not %q", input)}
				}
			}
		}
		if name == "-xlinker" && i+1 < len(linkInputs) {
			linkerArg := strings.ToLower(strings.TrimSpace(linkInputs[i+1]))
			if linkerArg == "-l" || linkerArg == "-framework" {
				if i+3 < len(linkInputs) && strings.EqualFold(strings.TrimSpace(linkInputs[i+2]), "-Xlinker") && isLibopusLibraryName(strings.TrimSpace(linkInputs[i+3])) {
					return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("helper must use its selected libopus archive, not -Xlinker %s -Xlinker opus", linkerArg)}
				}
			}
		}
	}
	return nil
}

func isLibopusLinkInput(name string) bool {
	return name == "-lopus" || (strings.HasPrefix(name, "-l:") && strings.Contains(name, "opus")) ||
		(strings.Contains(name, "libopus") && (strings.HasSuffix(name, ".a") || strings.HasSuffix(name, ".dylib") || strings.HasSuffix(name, ".so") || strings.Contains(name, ".so."))) ||
		strings.HasSuffix(name, ".a")
}

func isLibopusLibraryName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "opus" || name == "libopus" || isLibopusLinkInput(name)
}

func dnnReferenceSourcePaths(sourceDir string, sources []string) []string {
	paths := make([]string, len(sources))
	for i, source := range sources {
		paths[i] = filepath.Join(sourceDir, filepath.FromSlash(source))
	}
	return paths
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

// ProbeFloat2Int24 returns libopus float2int(8388608.f*sample) results from
// the selected target C implementation. It preserves that target's behavior
// for int32 overflow, infinities, NaNs, and tie rounding.
func ProbeFloat2Int24(samples []float32) ([]int32, error) {
	helperPath, err := floatQuantHelper()
	if err != nil {
		return nil, err
	}

	payload := NewOraclePayload("GFQI", FloatQuantModeFloat2Int24, uint32(len(samples)))
	for _, sample := range samples {
		payload.Float32(sample)
	}

	reader, err := RunOracle(helperPath, payload.Bytes(), "float2int24", "GFQO")
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
