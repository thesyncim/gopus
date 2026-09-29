package libopustest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

type CHelperConfig struct {
	Label              string
	OutputBase         string
	SourceFile         string
	ProbeRelPath       string
	CFlags             []string
	RefIncludes        []string
	QEXTRef            bool
	FixedRef           bool
	FixedQEXTRef       bool
	DREDQEXTRef        bool
	CustomRef          bool
	CustomQEXTRef      bool
	CustomFixedRef     bool
	CustomFixedQEXTRef bool
	// SIMDRef links the SIMD/RTCD-enabled libopus tree (opus-1.6.1-simd). Pair it
	// with a Go SIMD build or with a test that invokes the matching Go SIMD kernel.
	SIMDRef bool
	// ForceScalarRef compiles every RefSource (and the helper itself) against the
	// explicit scalar reference, with vectorization disabled and scalar kernel
	// headers selected instead of RTCD dispatch tables. Helpers that
	// compile a hand-picked subset of libopus .c files (no libopus.a link) and
	// reach silk_inner_product_FLP / silk_VQ_WMat_EC (or other x86-dispatched
	// kernels) need this: otherwise the default RTCD/intrinsics config routes
	// them through SILK_*_IMPL tables defined only in silk/x86/x86_silk_map.c +
	// the AVX2/SSE impls, which are not in the subset and fail to link on amd64
	// and Windows. Normal FMA contraction remains enabled.
	ForceScalarRef bool
	IncludeDirs    []string
	RefSources     []string
	Sources        []string
	Libs           []string
	LDFlags        []string
	DeadStrip      bool
}

// HelperCache caches a lazily built helper binary path for oracle tests.
type HelperCache struct {
	once sync.Once
	path string
	err  error
}

// Path returns the cached helper path, building it on the first call.
func (c *HelperCache) Path(build func() (string, error)) (string, error) {
	c.once.Do(func() {
		c.path, c.err = build()
	})
	if c.err != nil {
		return "", c.err
	}
	return c.path, nil
}

// CHelperPath returns the cached path for a C oracle helper built from cfg.
func (c *HelperCache) CHelperPath(cfg CHelperConfig) (string, error) {
	return c.Path(func() (string, error) {
		return BuildCHelper(cfg)
	})
}

func OracleEnabled() bool {
	if !StrictRefRequired() {
		switch strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_LIBOPUS_ORACLE"))) {
		case "0", "false", "off", "skip":
			return false
		}
	}
	if oracleBuildTagEnabled || StrictRefRequired() {
		return true
	}
	switch strings.TrimSpace(strings.ToLower(os.Getenv("GOPUS_TEST_TIER"))) {
	case "fast", "smoke":
		return false
	default:
		return true
	}
}

func RequireOracle(t testing.TB) {
	t.Helper()
	if !OracleEnabled() {
		t.Skip("libopus oracle disabled for this test tier")
	}
}

func HelperUnavailable(t testing.TB, label string, err error) {
	t.Helper()
	var configErr *libopustooling.LibopusReferenceConfigError
	if errors.As(err, &configErr) {
		t.Fatalf("libopus %s reference configuration is invalid: %v", label, err)
	}
	if StrictRefRequired() {
		t.Fatalf("libopus %s helper unavailable: %v", label, err)
	}
	t.Skipf("libopus %s helper unavailable: %v", label, err)
}

func BuildCHelper(cfg CHelperConfig) (string, error) {
	if cfg.Label == "" {
		cfg.Label = cfg.OutputBase
	}
	if cfg.OutputBase == "" || cfg.SourceFile == "" {
		return "", fmt.Errorf("helper output base and source file are required")
	}
	if err := validateCHelperReferenceSelection(cfg); err != nil {
		return "", err
	}
	targetCFlags, err := libopustooling.LibopusAMD64TargetCFlags()
	if err != nil {
		return "", err
	}
	if len(targetCFlags) != 0 {
		for _, flag := range append(append([]string(nil), cfg.CFlags...), cfg.LDFlags...) {
			if flag == "-march" || strings.HasPrefix(flag, "-march=") || flag == "-mtune" || strings.HasPrefix(flag, "-mtune=") {
				return "", &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("public C helper flags cannot override %s compiler target", libopustooling.LibopusAMD64TargetEnv)}
			}
		}
		cfg.CFlags = append(cfg.CFlags, targetCFlags...)
	}
	if cfg.DREDQEXTRef {
		if err := validateDREDReferenceBuildPairing(); err != nil {
			return "", err
		}
	}
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		return "", fmt.Errorf("cc not available: %w", err)
	}

	root := repoRoot()
	pairedVariant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", err
	}
	refVariant := pairedVariant
	if cfg.SIMDRef {
		refVariant = libopustooling.LibopusReferenceSIMD
	}
	if cfg.ForceScalarRef {
		refVariant = libopustooling.LibopusReferenceScalar
	}
	if cfg.QEXTRef {
		if cfg.SIMDRef && pairedVariant != libopustooling.LibopusReferenceSIMD {
			return "", &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("QEXT SIMD helper conflicts with the scalar Go reference lane")}
		}
		if refVariant == libopustooling.LibopusReferenceSIMD {
			refVariant = libopustooling.LibopusReferenceQEXTSIMD
		} else {
			refVariant = libopustooling.LibopusReferenceQEXTScalar
		}
	}
	if cfg.FixedRef {
		if cfg.SIMDRef && pairedVariant != libopustooling.LibopusReferenceSIMD {
			return "", &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("fixed SIMD helper conflicts with the scalar Go reference lane")}
		}
		if refVariant == libopustooling.LibopusReferenceSIMD {
			refVariant = libopustooling.LibopusReferenceFixedSIMD
		} else {
			refVariant = libopustooling.LibopusReferenceFixedScalar
		}
	}
	if cfg.FixedQEXTRef {
		if cfg.SIMDRef && pairedVariant != libopustooling.LibopusReferenceSIMD {
			return "", &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("fixed-QEXT SIMD helper conflicts with the scalar Go reference lane")}
		}
		if refVariant == libopustooling.LibopusReferenceSIMD {
			refVariant = libopustooling.LibopusReferenceFixedQEXTSIMD
		} else {
			refVariant = libopustooling.LibopusReferenceFixedQEXTScalar
		}
	}
	if cfg.DREDQEXTRef {
		if cfg.SIMDRef && pairedVariant != libopustooling.LibopusReferenceSIMD {
			return "", &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("DRED-QEXT SIMD helper conflicts with the scalar Go reference lane")}
		}
		refVariant, err = resolveDREDQEXTReferenceVariantForCurrentBuild()
		if err != nil {
			return "", err
		}
	}
	if cfg.CustomQEXTRef || cfg.CustomFixedRef || cfg.CustomFixedQEXTRef {
		if cfg.SIMDRef && pairedVariant != libopustooling.LibopusReferenceSIMD {
			return "", &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("custom SIMD helper conflicts with the scalar Go reference lane")}
		}
		if cfg.CustomQEXTRef {
			refVariant = libopustooling.LibopusReferenceCustomQEXTScalar
			if pairedVariant == libopustooling.LibopusReferenceSIMD {
				refVariant = libopustooling.LibopusReferenceCustomQEXTSIMD
			}
		} else if cfg.CustomFixedRef {
			refVariant = libopustooling.LibopusReferenceCustomFixedScalar
			if pairedVariant == libopustooling.LibopusReferenceSIMD {
				refVariant = libopustooling.LibopusReferenceCustomFixedSIMD
			}
		} else {
			refVariant = libopustooling.LibopusReferenceCustomFixedQEXTScalar
			if pairedVariant == libopustooling.LibopusReferenceSIMD {
				refVariant = libopustooling.LibopusReferenceCustomFixedQEXTSIMD
			}
		}
	}
	if target, err := libopustooling.ResolveLibopusAMD64Target(); err != nil {
		return "", err
	} else if err := validateCHelperAMD64TargetReference(target, cfg, refVariant); err != nil {
		return "", err
	}
	refDir := helperRefDir(cfg, refVariant)
	scalarRef := refVariant == libopustooling.LibopusReferenceScalar || refVariant == libopustooling.LibopusReferenceQEXTScalar || refVariant == libopustooling.LibopusReferenceFixedScalar || refVariant == libopustooling.LibopusReferenceFixedQEXTScalar || refVariant == libopustooling.LibopusReferenceDREDQEXTScalar || refVariant == libopustooling.LibopusReferenceCustomQEXTScalar || refVariant == libopustooling.LibopusReferenceCustomFixedScalar || refVariant == libopustooling.LibopusReferenceCustomFixedQEXTScalar
	ensureRef := libopustooling.EnsureLibopusScalar
	flavor := "scalar"
	if refVariant == libopustooling.LibopusReferenceSIMD {
		ensureRef = libopustooling.EnsureLibopusSIMD
		flavor = "simd"
	}
	if cfg.QEXTRef {
		ensureRef = libopustooling.EnsureLibopusQEXTScalar
		flavor = "qext-scalar"
		if refVariant == libopustooling.LibopusReferenceQEXTSIMD {
			ensureRef = libopustooling.EnsureLibopusQEXTSIMD
			flavor = "qext-simd"
		}
	}
	if cfg.FixedRef {
		ensureRef = libopustooling.EnsureLibopusFixedScalar
		flavor = "fixed-scalar"
		if refVariant == libopustooling.LibopusReferenceFixedSIMD {
			ensureRef = libopustooling.EnsureLibopusFixedSIMD
			flavor = "fixed-simd"
		}
	}
	if cfg.FixedQEXTRef {
		ensureRef = libopustooling.EnsureLibopusFixedQEXTScalar
		flavor = "fixed-qext-scalar"
		if refVariant == libopustooling.LibopusReferenceFixedQEXTSIMD {
			ensureRef = libopustooling.EnsureLibopusFixedQEXTSIMD
			flavor = "fixed-qext-simd"
		}
	}
	if cfg.DREDQEXTRef {
		ensureRef = libopustooling.EnsureLibopusDREDQEXTScalar
		flavor = "dred-qext-scalar"
		if refVariant == libopustooling.LibopusReferenceDREDQEXTSIMD {
			ensureRef = libopustooling.EnsureLibopusDREDQEXTSIMD
			flavor = "dred-qext-simd"
		}
	}
	if cfg.CustomRef {
		ensureRef = libopustooling.EnsureLibopusCustom
		flavor = "custom"
		if scalarRef {
			ensureRef = libopustooling.EnsureLibopusCustomScalar
			flavor = "custom-scalar"
		}
	}
	if cfg.CustomQEXTRef {
		ensureRef = libopustooling.EnsureLibopusCustomQEXTScalar
		flavor = "custom-qext-scalar"
		if !scalarRef {
			ensureRef = libopustooling.EnsureLibopusCustomQEXTSIMD
			flavor = "custom-qext-simd"
		}
	}
	if cfg.CustomFixedRef {
		ensureRef = libopustooling.EnsureLibopusCustomFixedScalar
		flavor = "custom-fixed-scalar"
		if !scalarRef {
			ensureRef = libopustooling.EnsureLibopusCustomFixedSIMD
			flavor = "custom-fixed-simd"
		}
	}
	if cfg.CustomFixedQEXTRef {
		ensureRef = libopustooling.EnsureLibopusCustomFixedQEXTScalar
		flavor = "custom-fixed-qext-scalar"
		if !scalarRef {
			ensureRef = libopustooling.EnsureLibopusCustomFixedQEXTSIMD
			flavor = "custom-fixed-qext-simd"
		}
	}
	if cfg.SIMDRef && !cfg.QEXTRef && !cfg.FixedRef && !cfg.FixedQEXTRef && !cfg.DREDQEXTRef && !cfg.CustomRef && !cfg.CustomQEXTRef && !cfg.CustomFixedRef && !cfg.CustomFixedQEXTRef {
		ensureRef = libopustooling.EnsureLibopusSIMD
		flavor = "simd"
	}
	if helperNeedsConfig(cfg.CFlags) {
		if _, err := os.Stat(filepath.Join(refDir, "config.h")); err != nil {
			ensureRef(libopustooling.DefaultVersion, []string{root})
		}
	}
	probeRel := cfg.ProbeRelPath
	if probeRel == "" {
		probeRel = "config.h"
	}
	if _, err := os.Stat(filepath.Join(refDir, filepath.FromSlash(probeRel))); err != nil {
		ensureRef(libopustooling.DefaultVersion, []string{root})
	}
	if helperReferenceLibMissing(cfg.Libs, refDir) {
		ensureRef(libopustooling.DefaultVersion, []string{root})
	}

	validateVariant := refVariant
	if cfg.CustomRef {
		validateVariant = libopustooling.LibopusReferenceCustomSIMD
		if scalarRef {
			validateVariant = libopustooling.LibopusReferenceCustomScalar
		}
	}
	if err := libopustooling.ValidateLibopusReferenceBuild(refDir, validateVariant, libopustooling.DefaultVersion); err != nil {
		ensureRef(libopustooling.DefaultVersion, []string{root})
		if err := libopustooling.ValidateLibopusReferenceBuild(refDir, validateVariant, libopustooling.DefaultVersion); err != nil {
			return "", err
		}
	}
	if err := validateHelperReferenceArchives(cfg.Libs, refDir, refVariant); err != nil {
		return "", err
	}

	srcPath := cfg.SourceFile
	if !filepath.IsAbs(srcPath) {
		srcPath = filepath.Join(root, "tools", "csrc", filepath.FromSlash(srcPath))
	}
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("%s helper source not found: %w", cfg.Label, err)
	}

	outDir := filepath.Join(os.TempDir(), "gopus_libopus_test_helpers")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir helper dir: %w", err)
	}
	digest := helperConfigDigest(cfg, refDir, srcPath, scalarRef)
	outPath := helperOutputPathWithDigest(outDir, cfg.OutputBase, cfg.SourceFile, flavor, digest)
	tmpFile, err := os.CreateTemp(outDir, filepath.Base(outPath)+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create helper temp output: %w", err)
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close helper temp output: %w", err)
	}
	if err := os.Remove(tmpPath); err != nil {
		return "", fmt.Errorf("remove helper temp placeholder: %w", err)
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	args := helperCCompileFlags(cfg, scalarRef)
	args = append(args, "-I", refDir, "-I", filepath.Join(refDir, "include"))
	for _, rel := range cfg.RefIncludes {
		args = append(args, "-I", filepath.Join(refDir, filepath.FromSlash(rel)))
	}
	for _, inc := range cfg.IncludeDirs {
		args = append(args, "-I", inc)
	}
	// An explicit SIMD reference keeps its platform dispatch even when other
	// tests in the same Go build use the paired scalar reference.
	if cfg.ForceScalarRef || (scalarRef && !cfg.SIMDRef) {
		// libopus's config.h has no include guard, so each compiled .c re-defines
		// the x86 feature macros (OPUS_X86_MAY_HAVE_SSE4_1, ...) -- clearing them
		// via -include is undone. Instead pre-define the SIMD headers' own include
		// guards so their bodies are skipped: silk/main.h then falls through to the
		// scalar silk_inner_product_FLP / silk_VQ_WMat_EC macros (the _c kernels),
		// which avoids referencing the RTCD
		// dispatch tables that are absent from a hand-picked RefSource subset.
		args = append(args, forceScalarRefDefines()...)
	}
	args = append(args, srcPath)
	for _, rel := range cfg.RefSources {
		args = append(args, filepath.Join(refDir, filepath.FromSlash(rel)))
	}
	args = append(args, cfg.Sources...)
	libs := cfg.Libs
	if len(libs) == 0 {
		libs = []string{"-lm"}
	}
	args = append(args, libs...)
	if scalarRef && runtime.GOOS == "linux" {
		// Some helpers link the scalar libopus.a (which pulls celt.o defining
		// celt_fatal) while their own source also provides a no-op celt_fatal stub.
		// Let the stub win rather than erroring on the duplicate symbol (GNU ld).
		args = append(args, "-Wl,--allow-multiple-definition")
	}
	if cfg.DeadStrip {
		if runtime.GOOS == "darwin" {
			args = append(args, "-Wl,-dead_strip")
		} else {
			args = append(args, "-Wl,--gc-sections")
		}
	}
	args = append(args, cfg.LDFlags...)
	args = append(args, "-o", tmpPath)

	cmd := exec.Command(ccPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s helper: %w (%s)", cfg.Label, err, bytes.TrimSpace(output))
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(outPath)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		return "", fmt.Errorf("install %s helper: %w", cfg.Label, err)
	}
	return outPath, nil
}

// helperCCompileFlags keeps the compiler's default C dialect and floating-point
// contraction policy, matching how the paired libopus reference is built.
func helperCCompileFlags(cfg CHelperConfig, scalarRef bool) []string {
	args := []string{"-O2"}
	if cfg.DeadStrip {
		args = append(args, "-ffunction-sections", "-fdata-sections")
	}
	args = append(args, cfg.CFlags...)
	if scalarRef {
		args = append(args, strings.Fields(libopustooling.LibopusScalarCVectorizationFlags)...)
	}
	return args
}

func validateCHelperReferenceSelection(cfg CHelperConfig) error {
	selected := 0
	for _, enabled := range []bool{cfg.QEXTRef, cfg.FixedRef, cfg.FixedQEXTRef, cfg.DREDQEXTRef, cfg.CustomRef, cfg.CustomQEXTRef, cfg.CustomFixedRef, cfg.CustomFixedQEXTRef} {
		if enabled {
			selected++
		}
	}
	if selected > 1 {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("QEXT, fixed-point, fixed-QEXT, DRED-QEXT, custom, custom-QEXT, custom-fixed, and custom-fixed-QEXT references are mutually exclusive")}
	}
	if cfg.ForceScalarRef && (cfg.SIMDRef || cfg.FixedRef || cfg.FixedQEXTRef || cfg.QEXTRef || cfg.DREDQEXTRef || cfg.CustomQEXTRef || cfg.CustomFixedRef || cfg.CustomFixedQEXTRef) {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("ForceScalarRef cannot be combined with SIMD, fixed-point, or QEXT references")}
	}
	return nil
}

func validateCHelperAMD64TargetReference(target string, cfg CHelperConfig, variant libopustooling.LibopusReferenceVariant) error {
	if target == "" {
		return nil
	}
	if cfg.QEXTRef || cfg.FixedRef || cfg.FixedQEXTRef || cfg.DREDQEXTRef || cfg.CustomRef ||
		cfg.CustomQEXTRef || cfg.CustomFixedRef || cfg.CustomFixedQEXTRef ||
		(variant != libopustooling.LibopusReferenceScalar && variant != libopustooling.LibopusReferenceSIMD) {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("%s supports only the default float-core scalar or SIMD reference", libopustooling.LibopusAMD64TargetEnv)}
	}
	return nil
}

func helperOutputPath(dir, outputBase, sourceFile, flavor string) string {
	return helperOutputPathForGOOS(dir, outputBase, sourceFile, flavor, runtime.GOOS, runtime.GOARCH)
}

func helperOutputPathWithDigest(dir, outputBase, sourceFile, flavor, digest string) string {
	return helperOutputPathForGOOSWithDigest(dir, outputBase, sourceFile, flavor, runtime.GOOS, runtime.GOARCH, digest)
}

func helperOutputPathForGOOS(dir, outputBase, sourceFile, flavor, goos, goarch string) string {
	return helperOutputPathForGOOSWithDigest(dir, outputBase, sourceFile, flavor, goos, goarch, "")
}

func helperOutputPathForGOOSWithDigest(dir, outputBase, sourceFile, flavor, goos, goarch, digest string) string {
	stem := strings.TrimSuffix(filepath.Base(filepath.FromSlash(sourceFile)), filepath.Ext(sourceFile))
	base := fmt.Sprintf("%s_%s_%s_%s_%s", outputBase, stem, flavor, goos, goarch)
	if digest != "" {
		base += "_" + digest
	}
	if goos == "windows" {
		base += ".exe"
	}
	return filepath.Join(dir, base)
}

func helperNeedsConfig(cflags []string) bool {
	for _, flag := range cflags {
		if flag == "-DHAVE_CONFIG_H" || flag == "-DHAVE_CONFIG_H=1" {
			return true
		}
	}
	return false
}

func helperRefDir(cfg CHelperConfig, pairedVariant libopustooling.LibopusReferenceVariant) string {
	if cfg.FixedQEXTRef {
		switch pairedVariant {
		case libopustooling.LibopusReferenceScalar:
			pairedVariant = libopustooling.LibopusReferenceFixedQEXTScalar
		case libopustooling.LibopusReferenceSIMD:
			pairedVariant = libopustooling.LibopusReferenceFixedQEXTSIMD
		}
		suffix, err := libopustooling.LibopusReferenceSourceSuffix(pairedVariant)
		if err != nil {
			panic(err)
		}
		return filepath.Join(repoRoot(), "tmp_check", "opus-"+libopustooling.DefaultVersion+suffix)
	}
	if cfg.DREDQEXTRef {
		switch pairedVariant {
		case libopustooling.LibopusReferenceScalar:
			pairedVariant = libopustooling.LibopusReferenceDREDQEXTScalar
		case libopustooling.LibopusReferenceSIMD:
			pairedVariant = libopustooling.LibopusReferenceDREDQEXTSIMD
		}
		suffix, err := libopustooling.LibopusReferenceSourceSuffix(pairedVariant)
		if err != nil {
			panic(err)
		}
		return filepath.Join(repoRoot(), "tmp_check", "opus-"+libopustooling.DefaultVersion+suffix)
	}
	if cfg.FixedRef {
		switch pairedVariant {
		case libopustooling.LibopusReferenceScalar:
			pairedVariant = libopustooling.LibopusReferenceFixedScalar
		case libopustooling.LibopusReferenceSIMD:
			pairedVariant = libopustooling.LibopusReferenceFixedSIMD
		}
		suffix, err := libopustooling.LibopusReferenceSourceSuffix(pairedVariant)
		if err != nil {
			panic(err)
		}
		return filepath.Join(repoRoot(), "tmp_check", "opus-"+libopustooling.DefaultVersion+suffix)
	}
	if cfg.QEXTRef {
		switch pairedVariant {
		case libopustooling.LibopusReferenceScalar:
			pairedVariant = libopustooling.LibopusReferenceQEXTScalar
		case libopustooling.LibopusReferenceSIMD:
			pairedVariant = libopustooling.LibopusReferenceQEXTSIMD
		}
		suffix, err := libopustooling.LibopusReferenceSourceSuffix(pairedVariant)
		if err != nil {
			panic(err)
		}
		return filepath.Join(repoRoot(), "tmp_check", "opus-"+libopustooling.DefaultVersion+suffix)
	}
	if cfg.CustomRef {
		if pairedVariant == libopustooling.LibopusReferenceScalar {
			return CustomScalarRefPath()
		}
		return filepath.Join(repoRoot(), "tmp_check", "opus-"+libopustooling.DefaultVersion+"-custom")
	}
	if cfg.SIMDRef {
		return SIMDRefPath()
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(pairedVariant)
	if err != nil {
		panic(err)
	}
	return filepath.Join(repoRoot(), "tmp_check", "opus-"+libopustooling.DefaultVersion+suffix)
}

func validateHelperReferenceArchives(libs []string, refDir string, variant libopustooling.LibopusReferenceVariant) error {
	want := filepath.Clean(filepath.Join(refDir, ".libs", "libopus.a"))
	for _, lib := range libs {
		if filepath.Base(filepath.FromSlash(lib)) != "libopus.a" {
			continue
		}
		got := filepath.Clean(lib)
		if !filepath.IsAbs(got) || got != want {
			return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("libopus archive %q does not match %s reference headers in %s", lib, variant, refDir)}
		}
	}
	return nil
}

func helperReferenceLibMissing(libs []string, refDir string) bool {
	refDir = filepath.Clean(refDir)
	for _, lib := range libs {
		if lib == "" || strings.HasPrefix(lib, "-") || !filepath.IsAbs(lib) {
			continue
		}
		lib = filepath.Clean(lib)
		rel, err := filepath.Rel(refDir, lib)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		if _, err := os.Stat(lib); err != nil {
			return true
		}
	}
	return false
}

// forceScalarRefDefines pre-defines the libopus SIMD headers' include guards so
// their bodies (which override the SILK/CELT kernel macros with RTCD dispatch
// tables) are skipped, leaving the scalar _c kernel macros from silk/main.h and
// celt headers in effect. MAIN_SSE_H guards silk/x86/main_sse.h (the SILK FLP
// inner-product / VQ_WMat_EC dispatch); VQ_SSE_H, PITCH_SSE_H and CELT_LPC_SSE_H
// guard the celt/x86 headers that redirect op_pvq_search, the pitch
// inner-products, and celt_lpc/celt_fir to their _sse* forms (whose objects are
// absent from a --disable-intrinsics scalar libopus.a). This works even though
// config.h lacks an include guard and is re-included per translation unit.
func forceScalarRefDefines() []string {
	return []string{"-DMAIN_SSE_H=1", "-DVQ_SSE_H=1", "-DPITCH_SSE_H=1", "-DCELT_LPC_SSE_H=1"}
}

func helperConfigDigest(cfg CHelperConfig, refDir, srcPath string, scalarRef bool) string {
	h := sha256.New()
	helperHashString(h, "v5")
	helperHashString(h, cfg.OutputBase)
	helperHashString(h, cfg.SourceFile)
	helperHashString(h, fmt.Sprintf("dead-strip=%t", cfg.DeadStrip))
	helperHashString(h, fmt.Sprintf("qext-ref=%t", cfg.QEXTRef))
	helperHashString(h, fmt.Sprintf("fixed-ref=%t", cfg.FixedRef))
	helperHashString(h, fmt.Sprintf("fixed-qext-ref=%t", cfg.FixedQEXTRef))
	helperHashString(h, fmt.Sprintf("dred-qext-ref=%t", cfg.DREDQEXTRef))
	helperHashString(h, fmt.Sprintf("custom-ref=%t", cfg.CustomRef))
	helperHashString(h, fmt.Sprintf("custom-qext-ref=%t", cfg.CustomQEXTRef))
	helperHashString(h, fmt.Sprintf("custom-fixed-ref=%t", cfg.CustomFixedRef))
	helperHashString(h, fmt.Sprintf("custom-fixed-qext-ref=%t", cfg.CustomFixedQEXTRef))
	helperHashString(h, fmt.Sprintf("simd-ref=%t", cfg.SIMDRef))
	helperHashString(h, fmt.Sprintf("force-scalar-ref=%t", cfg.ForceScalarRef))
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		helperHashString(h, "paired-reference-error="+err.Error())
	} else {
		helperHashString(h, "paired-reference="+string(variant))
	}
	helperHashStrings(h, "cflags", cfg.CFlags)
	helperHashStrings(h, "base-compile-flags", helperCCompileFlags(cfg, scalarRef))
	helperHashStrings(h, "ref-includes", cfg.RefIncludes)
	helperHashStrings(h, "include-dirs", cfg.IncludeDirs)
	helperHashStrings(h, "ref-sources", cfg.RefSources)
	helperHashStrings(h, "sources", cfg.Sources)
	helperHashStrings(h, "libs", cfg.Libs)
	helperHashStrings(h, "ldflags", cfg.LDFlags)
	helperHashFile(h, "source", srcPath)
	helperHashFile(h, "config", filepath.Join(refDir, "config.h"))
	helperHashFile(h, "build-stamp", filepath.Join(refDir, ".gopus-libopus-build"))
	for _, rel := range cfg.RefSources {
		helperHashFile(h, "ref-source", filepath.Join(refDir, filepath.FromSlash(rel)))
	}
	for _, src := range cfg.Sources {
		helperHashFile(h, "source-extra", src)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func helperHashStrings(h hash.Hash, label string, values []string) {
	helperHashString(h, label)
	for _, value := range values {
		helperHashString(h, value)
	}
}

func helperHashString(h hash.Hash, value string) {
	_, _ = h.Write([]byte(value))
	_, _ = h.Write([]byte{0})
}

func helperHashFile(h hash.Hash, label, path string) {
	helperHashString(h, label)
	helperHashString(h, path)
	data, err := os.ReadFile(path)
	if err != nil {
		helperHashString(h, "missing")
		helperHashString(h, err.Error())
		return
	}
	_, _ = h.Write(data)
	_, _ = h.Write([]byte{0})
}

func RunHelper(binPath string, input []byte) ([]byte, error) {
	return RunHelperEnv(binPath, input, nil)
}

func RunHelperArgs(binPath string, input []byte, args ...string) ([]byte, error) {
	return runHelper(binPath, input, nil, args)
}

func RunHelperEnv(binPath string, input []byte, env []string) ([]byte, error) {
	return runHelper(binPath, input, env, nil)
}

func RunHelperArgsEnv(binPath string, input []byte, env []string, args ...string) ([]byte, error) {
	return runHelper(binPath, input, env, args)
}

func runHelper(binPath string, input []byte, env []string, args []string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(binPath, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run helper: %w (%s)", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}
